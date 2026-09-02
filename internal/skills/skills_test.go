package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/contract"
)

func scratchDir(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "work")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "skill-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	absolute, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	return absolute
}

func TestDryRunDoesNotCreateDestination(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "missing", "skills", "slack")
	result, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Applied {
		t.Fatalf("bad result: %+v", result)
	}
	if _, err := os.Stat(filepath.Dir(destination)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run mutated parent: %v", err)
	}
}

func TestCodexAndClaudeInstallIdenticalCanonicalBytes(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	codex := filepath.Join(root, "codex", "slack")
	claude := filepath.Join(root, "claude", "slack")
	for _, item := range []struct {
		destination string
		provider    Provider
	}{{codex, ProviderCodex}, {claude, ProviderClaude}} {
		result, err := Install(context.Background(), item.destination, item.provider, ScopeProject, true)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Applied || !result.Changed {
			t.Fatalf("bad result: %+v", result)
		}
	}
	left, err := fileDigests(codex)
	if err != nil {
		t.Fatal(err)
	}
	right, err := fileDigests(claude)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != len(right) {
		t.Fatalf("file counts differ")
	}
	for path, digest := range left {
		if right[path] != digest {
			t.Fatalf("%s differs", path)
		}
	}
}

func TestModifiedOwnedFileBlocksUpgradeAndUninstall(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "skills", "slack")
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	skillFile := filepath.Join(destination, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("upgrade should conflict: %v", err)
	}
	if _, err := Uninstall(context.Background(), destination, ProviderCodex, ScopeProject, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("uninstall should conflict: %v", err)
	}
	payload, err := os.ReadFile(skillFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != "modified" {
		t.Fatal("modified file was overwritten")
	}
}

func TestSymlinkDestinationRejected(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "slack")
	if err := os.Symlink(target, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestSymlinkParentRejectedWithoutCreatingOutsideAnchor(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedParent := filepath.Join(root, "linked")
	if err := os.Symlink(outside, linkedParent); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(linkedParent, "skills", "slack")
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, true); err == nil {
		t.Fatal("symlink parent accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "skills")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installer created a directory through symlink: %v", err)
	}
}

func TestOversizedManifestAndOwnedFileAreRejected(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "skills", "slack")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(destination, ".slack-agent-cli-manifest.json")
	if err := os.WriteFile(manifestPath, make([]byte, contract.MaxSkillManifestBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, false); err == nil {
		t.Fatal("oversized ownership manifest was accepted")
	}
	if err := os.RemoveAll(destination); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "SKILL.md"), make([]byte, contract.MaxSkillFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(context.Background(), destination, ProviderCodex, ScopeProject, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("oversized owned file was accepted: %v", err)
	}
}

func TestInterruptedInstallAndUninstallStatesRecoverSafely(t *testing.T) {
	t.Parallel()
	root := scratchDir(t)
	destination := filepath.Join(root, "skills", "slack")
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	backupSource := filepath.Join(root, "backup-source")
	if _, err := Install(context.Background(), backupSource, ProviderCodex, ScopeProject, true); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(backupSource, destination+".previous"); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("dry-run ignored interrupted install: %v", err)
	}
	if result, err := Install(context.Background(), destination, ProviderCodex, ScopeProject, true); err != nil || !result.Applied {
		t.Fatalf("install recovery result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(destination + ".previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup was not cleaned: %v", err)
	}
	if err := os.Rename(destination, destination+".removing"); err != nil {
		t.Fatal(err)
	}
	if _, err := Uninstall(context.Background(), destination, ProviderCodex, ScopeProject, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("dry-run ignored interrupted uninstall: %v", err)
	}
	if result, err := Uninstall(context.Background(), destination, ProviderCodex, ScopeProject, true); err != nil || !result.Applied || !result.Changed {
		t.Fatalf("uninstall recovery result=%+v err=%v", result, err)
	}
}

func fileDigests(root string) (map[string]string, error) {
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(payload)
		result[filepath.ToSlash(relative)] = hex.EncodeToString(sum[:])
		return nil
	})
	return result, err
}
