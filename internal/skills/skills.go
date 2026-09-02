// Package skills installs one canonical Skill for Codex or Claude Code.
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/abigotado/slack-agent-cli/assets"
	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/lockfile"
	"github.com/abigotado/slack-agent-cli/internal/safefile"
)

// ErrConflict marks an existing, modified, or unowned destination that the
// installer refuses to overwrite or remove.
var ErrConflict = errors.New("skill ownership conflict")

// CommitError reports a durability or cleanup failure after the requested
// Skill state became visible. Callers must inspect state instead of retrying.
type CommitError struct{ cause error }

func (e *CommitError) Error() string { return "skill change committed with a recovery warning" }
func (e *CommitError) Unwrap() error { return e.cause }

// WasCommitted reports whether the requested Skill state became visible.
func WasCommitted(err error) bool {
	var target *CommitError
	return errors.As(err, &target)
}

type Provider string

const (
	ProviderCodex  Provider = "codex"
	ProviderClaude Provider = "claude"
)

type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// Result describes a local Skill lifecycle operation.
type Result struct {
	Destination string   `json:"destination"`
	Provider    Provider `json:"provider"`
	Scope       Scope    `json:"scope"`
	Changed     bool     `json:"changed"`
	Applied     bool     `json:"applied"`
	Files       int      `json:"files"`
}

type manifest struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

// Destination resolves an allowlisted provider/scope location.
func Destination(provider Provider, scope Scope, projectDir string) (string, error) {
	var root, relative string
	switch scope {
	case ScopeUser:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = home
		switch provider {
		case ProviderCodex:
			relative = filepath.Join(".agents", "skills", "slack")
		case ProviderClaude:
			relative = filepath.Join(".claude", "skills", "slack")
		default:
			return "", errors.New("provider must be codex or claude")
		}
	case ScopeProject:
		if projectDir == "" {
			return "", errors.New("--project-dir is required for project scope")
		}
		absolute, err := filepath.Abs(projectDir)
		if err != nil {
			return "", err
		}
		root = absolute
		switch provider {
		case ProviderCodex:
			relative = filepath.Join(".agents", "skills", "slack")
		case ProviderClaude:
			relative = filepath.Join(".claude", "skills", "slack")
		default:
			return "", errors.New("provider must be codex or claude")
		}
	default:
		return "", errors.New("scope must be user or project")
	}
	if err := secureAnchor(root); err != nil {
		return "", err
	}
	destination := filepath.Clean(filepath.Join(root, relative))
	if !within(root, destination) {
		return "", errors.New("skill destination escaped its anchor")
	}
	return destination, nil
}

// Install validates ownership and installs the canonical bytes when apply is true.
func Install(ctx context.Context, destination string, provider Provider, scope Scope, apply bool) (Result, error) {
	files, err := canonicalFiles()
	if err != nil {
		return Result{}, err
	}
	result := Result{Destination: destination, Provider: provider, Scope: scope, Files: len(files)}
	desired := buildManifest(files)
	if !apply {
		if _, err := os.Lstat(destination + ".previous"); err == nil {
			return Result{}, fmt.Errorf("%w: interrupted install requires an applied recovery", ErrConflict)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		current, exists, err := readOwned(destination)
		if err != nil {
			return Result{}, err
		}
		if exists {
			if err := verifyOwned(destination, current); err != nil {
				return Result{}, err
			}
		}
		result.Changed = !exists || !equalManifest(current, desired)
		return result, nil
	}
	parent := filepath.Dir(destination)
	if err := secureMkdirAll(parent); err != nil {
		return Result{}, err
	}
	err = lockfile.With(ctx, filepath.Join(parent, ".slack-agent-cli-skill.lock"), func() error {
		if err := recoverInstallState(destination, desired); err != nil {
			return err
		}
		current, exists, err := readOwned(destination)
		if err != nil {
			return err
		}
		if exists {
			if err := verifyOwned(destination, current); err != nil {
				return err
			}
		}
		if exists && equalManifest(current, desired) {
			if err := syncDirectory(parent); err != nil {
				return &CommitError{cause: fmt.Errorf("sync existing Skill directory: %w", err)}
			}
			result.Applied = true
			return nil
		}
		result.Changed = true
		if err := commit(destination, files, desired, current, exists); err != nil {
			return err
		}
		result.Applied = true
		return nil
	})
	if err != nil && result.Applied && !WasCommitted(err) {
		err = &CommitError{cause: err}
	}
	return result, err
}

// Uninstall removes only an intact owned installation.
func Uninstall(ctx context.Context, destination string, provider Provider, scope Scope, apply bool) (Result, error) {
	result := Result{Destination: destination, Provider: provider, Scope: scope}
	if !apply {
		if _, err := os.Lstat(destination + ".removing"); err == nil {
			return Result{}, fmt.Errorf("%w: interrupted uninstall requires an applied recovery", ErrConflict)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
		current, exists, err := readOwned(destination)
		if err != nil {
			return Result{}, err
		}
		if !exists {
			return result, nil
		}
		if err := verifyOwned(destination, current); err != nil {
			return Result{}, err
		}
		result.Files = len(current.Files)
		result.Changed = true
		return result, nil
	}
	parent := filepath.Dir(destination)
	if err := secureMkdirAll(parent); err != nil {
		return Result{}, err
	}
	err := lockfile.With(ctx, filepath.Join(parent, ".slack-agent-cli-skill.lock"), func() error {
		if recovered, files, err := recoverUninstallState(destination); err != nil {
			return err
		} else if recovered {
			result.Files = files
			result.Changed = true
			result.Applied = true
			return nil
		}
		current, exists, err := readOwned(destination)
		if err != nil {
			return err
		}
		if !exists {
			if err := syncDirectory(parent); err != nil {
				return &CommitError{cause: fmt.Errorf("sync absent Skill directory: %w", err)}
			}
			return nil
		}
		result.Files = len(current.Files)
		if err := verifyOwned(destination, current); err != nil {
			return err
		}
		quarantine := destination + ".removing"
		if _, err := os.Lstat(quarantine); err == nil {
			return fmt.Errorf("%w: stale uninstall quarantine", ErrConflict)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := renameNoReplace(destination, quarantine); err != nil {
			return err
		}
		if err := verifyOwned(quarantine, current); err != nil {
			if restoreErr := renameNoReplace(quarantine, destination); restoreErr != nil {
				return fmt.Errorf("%w: uninstall race detected and restore failed: %v", err, restoreErr)
			}
			return err
		}
		result.Changed = true
		if err := os.RemoveAll(quarantine); err != nil {
			return &CommitError{cause: fmt.Errorf("owned skill quarantined but cleanup failed: %w", err)}
		}
		if err := syncDirectory(parent); err != nil {
			return &CommitError{cause: fmt.Errorf("sync removed Skill directory: %w", err)}
		}
		result.Applied = true
		return nil
	})
	if err != nil && result.Applied && !WasCommitted(err) {
		err = &CommitError{cause: err}
	}
	return result, err
}

func canonicalFiles() (map[string][]byte, error) {
	root, err := fs.Sub(assets.Skill, "skills/slack")
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	err = fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		payload, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		clean := filepath.ToSlash(filepath.Clean(path))
		if clean == "." || strings.HasPrefix(clean, "../") {
			return errors.New("embedded skill path escaped root")
		}
		files[clean] = payload
		return nil
	})
	return files, err
}
func buildManifest(files map[string][]byte) manifest {
	hashes := map[string]string{}
	for path, payload := range files {
		sum := sha256.Sum256(payload)
		hashes[path] = hex.EncodeToString(sum[:])
	}
	return manifest{Version: 1, Files: hashes}
}
func manifestBytes(value manifest) ([]byte, error) { return json.Marshal(value) }
func equalManifest(left, right manifest) bool {
	if left.Version != right.Version || len(left.Files) != len(right.Files) {
		return false
	}
	for path, digest := range left.Files {
		if right.Files[path] != digest {
			return false
		}
	}
	return true
}
func readOwned(destination string) (manifest, bool, error) {
	info, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return manifest{}, false, nil
	}
	if err != nil {
		return manifest{}, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return manifest{}, false, fmt.Errorf("%w: destination is not an owned directory", ErrConflict)
	}
	payload, _, err := safefile.ReadRegular(filepath.Join(destination, ".slack-agent-cli-manifest.json"), contract.MaxSkillManifestBytes)
	if errors.Is(err, os.ErrNotExist) {
		return manifest{}, false, fmt.Errorf("%w: existing skill has no ownership manifest", ErrConflict)
	}
	if err != nil {
		return manifest{}, false, err
	}
	var value manifest
	if err := json.Unmarshal(payload, &value); err != nil {
		return manifest{}, false, fmt.Errorf("%w: ownership manifest is invalid", ErrConflict)
	}
	if err := validateManifest(value); err != nil {
		return manifest{}, false, fmt.Errorf("%w: unsupported ownership manifest", ErrConflict)
	}
	return value, true, nil
}
func verifyOwned(destination string, value manifest) error {
	if err := validateManifest(value); err != nil {
		return fmt.Errorf("%w: invalid ownership manifest", ErrConflict)
	}
	expected := map[string]bool{".slack-agent-cli-manifest.json": true}
	allowedDirectories := map[string]bool{".": true}
	totalBytes := 0
	for path, digest := range value.Files {
		expected[filepath.ToSlash(path)] = true
		for directory := filepath.ToSlash(filepath.Dir(path)); directory != "."; directory = filepath.ToSlash(filepath.Dir(directory)) {
			allowedDirectories[directory] = true
		}
		full := filepath.Join(destination, filepath.FromSlash(path))
		if !within(destination, full) {
			return fmt.Errorf("%w: owned path escaped destination", ErrConflict)
		}
		payload, _, err := safefile.ReadRegular(full, contract.MaxSkillFileBytes)
		if err != nil {
			return fmt.Errorf("%w: owned file is missing or unsafe", ErrConflict)
		}
		totalBytes += len(payload)
		if totalBytes > contract.MaxSkillTotalBytes {
			return fmt.Errorf("%w: owned files exceed total byte bound", ErrConflict)
		}
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != digest {
			return fmt.Errorf("%w: owned file was modified", ErrConflict)
		}
	}
	return walkOwnedBounded(destination, expected, allowedDirectories)
}

func validateManifest(value manifest) error {
	if value.Version != 1 || len(value.Files) == 0 || len(value.Files) > contract.MaxSkillFiles {
		return errors.New("unsupported ownership manifest")
	}
	for path, digest := range value.Files {
		clean := filepath.ToSlash(filepath.Clean(path))
		if path != clean || path == "." || filepath.IsAbs(path) || strings.HasPrefix(path, "../") || len(path) > contract.MaxSkillPathBytes || len(strings.Split(path, "/")) > contract.MaxSkillDepth {
			return errors.New("ownership path is invalid")
		}
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("ownership digest is invalid")
		}
	}
	return nil
}

func walkOwnedBounded(root string, expectedFiles, allowedDirectories map[string]bool) error {
	type pendingDirectory struct {
		path  string
		depth int
	}
	pending := []pendingDirectory{{path: root}}
	entries := 0
	for len(pending) > 0 {
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		directory, err := os.Open(current.path)
		if err != nil {
			return err
		}
		for {
			batch, readErr := directory.ReadDir(16)
			for _, entry := range batch {
				entries++
				if entries > contract.MaxSkillEntries {
					_ = directory.Close()
					return fmt.Errorf("%w: skill tree exceeds entry bound", ErrConflict)
				}
				full := filepath.Join(current.path, entry.Name())
				relative, err := filepath.Rel(root, full)
				if err != nil || len(relative) > contract.MaxSkillPathBytes {
					_ = directory.Close()
					return fmt.Errorf("%w: skill tree path is invalid", ErrConflict)
				}
				relative = filepath.ToSlash(relative)
				if entry.Type()&os.ModeSymlink != 0 {
					_ = directory.Close()
					return fmt.Errorf("%w: skill tree contains a symlink", ErrConflict)
				}
				if entry.IsDir() {
					if current.depth+1 > contract.MaxSkillDepth || !allowedDirectories[relative] {
						_ = directory.Close()
						return fmt.Errorf("%w: skill tree contains an unowned directory", ErrConflict)
					}
					pending = append(pending, pendingDirectory{path: full, depth: current.depth + 1})
				} else if !expectedFiles[relative] {
					_ = directory.Close()
					return fmt.Errorf("%w: skill directory contains an unowned file", ErrConflict)
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = directory.Close()
				return readErr
			}
		}
		if err := directory.Close(); err != nil {
			return err
		}
	}
	return nil
}
func commit(destination string, files map[string][]byte, value, current manifest, exists bool) error {
	parent := filepath.Dir(destination)
	staging, err := os.MkdirTemp(parent, ".slack-skill-stage-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		full := filepath.Join(staging, filepath.FromSlash(path))
		if !within(staging, full) {
			return errors.New("skill path escaped staging")
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return err
		}
		if err := writeSyncedFile(full, files[path], 0o644); err != nil {
			return err
		}
	}
	payload, err := manifestBytes(value)
	if err != nil {
		return err
	}
	if err := writeSyncedFile(filepath.Join(staging, ".slack-agent-cli-manifest.json"), payload, 0o600); err != nil {
		return err
	}
	if err := syncTreeDirectories(staging); err != nil {
		return err
	}
	backup := destination + ".previous"
	if _, err := os.Lstat(backup); err == nil {
		return fmt.Errorf("%w: stale backup blocks install", ErrConflict)
	}
	if _, err := os.Lstat(destination); err == nil {
		if !exists {
			return fmt.Errorf("%w: destination appeared during install", ErrConflict)
		}
		if err := renameNoReplace(destination, backup); err != nil {
			return err
		}
		if err := verifyOwned(backup, current); err != nil {
			if restoreErr := renameNoReplace(backup, destination); restoreErr != nil {
				return fmt.Errorf("%w: install race detected and restore failed: %v", err, restoreErr)
			}
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if exists {
		return fmt.Errorf("%w: destination disappeared during install", ErrConflict)
	}
	if err := renameNoReplace(staging, destination); err != nil {
		if exists {
			_ = renameNoReplace(backup, destination)
		}
		return err
	}
	if exists {
		if err := os.RemoveAll(backup); err != nil {
			return &CommitError{cause: fmt.Errorf("new Skill committed but old backup cleanup failed: %w", err)}
		}
	}
	if err := syncDirectory(parent); err != nil {
		return &CommitError{cause: fmt.Errorf("sync committed Skill directory: %w", err)}
	}
	return nil
}

func recoverInstallState(destination string, desired manifest) error {
	backup := destination + ".previous"
	backupManifest, backupExists, err := readOwned(backup)
	if err != nil || !backupExists {
		if err == nil {
			return nil
		}
		return err
	}
	if err := verifyOwned(backup, backupManifest); err != nil {
		return err
	}
	current, destinationExists, err := readOwned(destination)
	if err != nil {
		return err
	}
	if !destinationExists {
		if err := renameNoReplace(backup, destination); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(destination))
	}
	if err := verifyOwned(destination, current); err != nil {
		return err
	}
	if !equalManifest(current, desired) {
		return fmt.Errorf("%w: backup exists beside a non-current destination", ErrConflict)
	}
	if err := os.RemoveAll(backup); err != nil {
		return &CommitError{cause: fmt.Errorf("current Skill is visible but backup cleanup failed: %w", err)}
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return &CommitError{cause: fmt.Errorf("sync recovered Skill directory: %w", err)}
	}
	return nil
}

func recoverUninstallState(destination string) (bool, int, error) {
	quarantine := destination + ".removing"
	current, exists, err := readOwned(quarantine)
	if err != nil || !exists {
		return false, 0, err
	}
	if _, destinationErr := os.Lstat(destination); destinationErr == nil {
		return false, 0, fmt.Errorf("%w: destination and uninstall quarantine both exist", ErrConflict)
	} else if !errors.Is(destinationErr, os.ErrNotExist) {
		return false, 0, destinationErr
	}
	if err := verifyOwned(quarantine, current); err != nil {
		return false, 0, err
	}
	if err := os.RemoveAll(quarantine); err != nil {
		return false, 0, &CommitError{cause: fmt.Errorf("skill removal committed but quarantine cleanup failed: %w", err)}
	}
	if err := syncDirectory(filepath.Dir(destination)); err != nil {
		return false, 0, &CommitError{cause: fmt.Errorf("sync recovered Skill removal: %w", err)}
	}
	return true, len(current.Files), nil
}
func secureAnchor(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("skill anchor must be a real directory")
	}
	return nil
}
func secureMkdirAll(path string) error {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return errors.New("skill path must be absolute")
	}
	volume := filepath.VolumeName(clean)
	current := volume + string(filepath.Separator)
	relative := strings.TrimPrefix(clean, current)
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("skill path contains a symlink or non-directory")
		}
	}
	return nil
}

func writeSyncedFile(path string, payload []byte, mode fs.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func syncTreeDirectories(root string) error {
	directories := []string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			directories = append(directories, path)
		}
		return err
	}); err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, directory := range directories {
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
func within(root, path string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
