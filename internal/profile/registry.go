package profile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/lockfile"
	"github.com/abigotado/slack-agent-cli/internal/safefile"
)

const registryVersion = 1

type registryFile struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}

// Registry persists non-secret profile metadata.
type Registry struct{ path string }

// CommitError reports a durability failure after the new registry was renamed
// into place. Callers must not roll back a related credential in this case.
type CommitError struct{ cause error }

func (e *CommitError) Error() string { return "profile registry committed with a durability warning" }
func (e *CommitError) Unwrap() error { return e.cause }

// WasCommitted reports whether the new registry contents became visible.
func WasCommitted(err error) bool { var target *CommitError; return errors.As(err, &target) }

// RollbackError reports that a pre-commit external-state rollback failed.
type RollbackError struct{ cause error }

func (e *RollbackError) Error() string { return "external profile state rollback failed" }
func (e *RollbackError) Unwrap() error { return e.cause }

// RollbackFailed reports an inconsistent external/profile transaction.
func RollbackFailed(err error) bool {
	var target *RollbackError
	return errors.As(err, &target)
}

// Transaction is a locked in-memory registry edit.
type Transaction struct {
	file  *registryFile
	dirty bool
}

// Get returns one exact profile from the locked snapshot.
func (t *Transaction) Get(name string) (Profile, error) {
	for _, item := range t.file.Profiles {
		if item.Name == name {
			return item, nil
		}
	}
	return Profile{}, ErrNotFound
}

// Put creates or replaces one profile in the locked snapshot.
func (t *Transaction) Put(value Profile) error {
	if err := value.Validate(); err != nil {
		return err
	}
	for index := range t.file.Profiles {
		if t.file.Profiles[index].Name == value.Name {
			t.file.Profiles[index] = value
			t.dirty = true
			return nil
		}
	}
	if len(t.file.Profiles) >= contract.MaxProfiles {
		return errors.New("profile count exceeds v1 bound")
	}
	t.file.Profiles = append(t.file.Profiles, value)
	t.dirty = true
	return nil
}

// Remove deletes one profile from the locked snapshot.
func (t *Transaction) Remove(name string) error {
	for index, item := range t.file.Profiles {
		if item.Name == name {
			t.file.Profiles = append(t.file.Profiles[:index], t.file.Profiles[index+1:]...)
			t.dirty = true
			return nil
		}
	}
	return ErrNotFound
}

// NewRegistry creates a registry at an explicit testable path.
func NewRegistry(path string) *Registry { return &Registry{path: path} }

// DefaultRegistry resolves the user's standard config directory.
func DefaultRegistry() (*Registry, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("resolve config directory: %w", err)
	}
	return NewRegistry(filepath.Join(dir, "slack-agent-cli", "profiles.json")), nil
}

// WithLock serializes changes to one profile namespace.
func (r *Registry) WithLock(ctx context.Context, fn func() error) error {
	if err := r.ensureDirectory(); err != nil {
		return err
	}
	return lockfile.With(ctx, r.path+".lock", fn)
}

// Transact loads, edits, and atomically saves the registry under one lock.
func (r *Registry) Transact(ctx context.Context, fn func(*Transaction) error) error {
	return r.transact(ctx, fn, nil)
}

// TransactWithRollback runs rollback under the same registry lock when an edit
// fails before commit. It is used to keep external credential state serialized
// with profile metadata.
func (r *Registry) TransactWithRollback(ctx context.Context, fn func(*Transaction) error, rollback func() error) error {
	return r.transact(ctx, fn, rollback)
}

func (r *Registry) transact(ctx context.Context, fn func(*Transaction) error, rollback func() error) error {
	committed := false
	err := r.WithLock(ctx, func() error {
		file, err := r.load()
		if err != nil {
			return err
		}
		transaction := &Transaction{file: &file}
		err = fn(transaction)
		if err == nil && transaction.dirty {
			err = r.save(file)
		}
		if err == nil || WasCommitted(err) {
			committed = true
		}
		if err != nil && !WasCommitted(err) && rollback != nil {
			if rollbackErr := rollback(); rollbackErr != nil {
				return &RollbackError{cause: errors.Join(err, fmt.Errorf("rollback external profile state: %w", rollbackErr))}
			}
		}
		return err
	})
	if err != nil && committed && !WasCommitted(err) {
		return &CommitError{cause: err}
	}
	return err
}

// List returns profiles sorted by name.
func (r *Registry) List(ctx context.Context) ([]Profile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := r.load()
	if err != nil {
		return nil, err
	}
	profiles := append([]Profile(nil), file.Profiles...)
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles, nil
}

// Get loads one exact profile.
func (r *Registry) Get(ctx context.Context, name string) (Profile, error) {
	if err := ctx.Err(); err != nil {
		return Profile{}, err
	}
	if err := ValidateName(name); err != nil {
		return Profile{}, err
	}
	file, err := r.load()
	if err != nil {
		return Profile{}, err
	}
	for _, item := range file.Profiles {
		if item.Name == name {
			return item, nil
		}
	}
	return Profile{}, ErrNotFound
}

// Put atomically creates or replaces one profile.
func (r *Registry) Put(ctx context.Context, value Profile) error {
	return r.Transact(ctx, func(transaction *Transaction) error { return transaction.Put(value) })
}

// Remove atomically deletes one exact profile.
func (r *Registry) Remove(ctx context.Context, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	return r.Transact(ctx, func(transaction *Transaction) error { return transaction.Remove(name) })
}

func (r *Registry) load() (registryFile, error) {
	if err := r.ensureDirectory(); err != nil {
		return registryFile{}, err
	}
	payload, info, err := safefile.ReadRegular(r.path, contract.MaxRegistryBytes)
	if errors.Is(err, os.ErrNotExist) {
		return registryFile{Version: registryVersion}, nil
	}
	if err != nil {
		return registryFile{}, fmt.Errorf("read profile registry: %w", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return registryFile{}, errors.New("profile registry permissions are too broad")
	}
	var file registryFile
	if err := json.Unmarshal(payload, &file); err != nil {
		return registryFile{}, fmt.Errorf("decode profile registry: %w", err)
	}
	if file.Version != registryVersion {
		return registryFile{}, errors.New("unsupported profile registry version")
	}
	if len(file.Profiles) > contract.MaxProfiles {
		return registryFile{}, errors.New("profile registry exceeds count bound")
	}
	seenNames := map[string]bool{}
	for _, item := range file.Profiles {
		if err := item.Validate(); err != nil {
			return registryFile{}, fmt.Errorf("invalid profile metadata: %w", err)
		}
		if seenNames[item.Name] {
			return registryFile{}, errors.New("duplicate profile name")
		}
		seenNames[item.Name] = true
	}
	return file, nil
}

func (r *Registry) save(file registryFile) error {
	sort.Slice(file.Profiles, func(i, j int) bool { return file.Profiles[i].Name < file.Profiles[j].Name })
	payload, err := json.Marshal(file)
	if err != nil {
		return fmt.Errorf("encode profile registry: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(r.path), ".profiles-*")
	if err != nil {
		return fmt.Errorf("create profile registry temp file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryName, r.path); err != nil {
		return fmt.Errorf("commit profile registry: %w", err)
	}
	dir, err := os.Open(filepath.Dir(r.path))
	if err != nil {
		return &CommitError{cause: fmt.Errorf("open profile registry directory: %w", err)}
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return &CommitError{cause: fmt.Errorf("sync profile registry directory: %w", err)}
	}
	return nil
}

func (r *Registry) ensureDirectory() error {
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create profile directory: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("profile directory must be a real directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return errors.New("profile directory permissions are too broad")
	}
	return nil
}
