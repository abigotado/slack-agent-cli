// Package policy stores identity-bound exact conversation policies.
package policy

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
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/safefile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

var (
	ErrNotAllowed      = errors.New("conversation is not allowlisted")
	ErrBindingMismatch = errors.New("policy binding does not match profile")
	ErrSharedMismatch  = errors.New("conversation shared state changed")
	ErrWriteNeedsRead  = errors.New("write targets must be a subset of read targets")
	ErrRebindNotNeeded = errors.New("policy binding is already current")
)

// CommitError reports a durability failure after the new policy file became
// visible. Callers must inspect state instead of retrying the transition.
type CommitError struct{ cause error }

func (e *CommitError) Error() string { return "policy registry committed with a durability warning" }
func (e *CommitError) Unwrap() error { return e.cause }

// WasCommitted reports whether a policy transition became visible.
func WasCommitted(err error) bool {
	var target *CommitError
	return errors.As(err, &target)
}

type Kind string

const (
	Read  Kind = "read"
	Write Kind = "write"
)

// Target is one exact conversation and its fail-closed shared classification.
type Target struct {
	ConversationID string             `json:"conversation_id"`
	Shared         slack.SharedStatus `json:"shared"`
}

// Set is one profile's complete policy state.
type Set struct {
	ProfileName     string   `json:"profile"`
	WorkspaceID     string   `json:"workspace_id"`
	ProfileIdentity string   `json:"profile_identity"`
	Generation      string   `json:"generation"`
	Reads           []Target `json:"reads"`
	Writes          []Target `json:"writes"`
}

type policyFile struct {
	Version int   `json:"version"`
	Sets    []Set `json:"sets"`
}

// Registry persists both policy kinds in one atomic file.
type Registry struct{ path string }

func NewRegistry(path string) *Registry { return &Registry{path: path} }

func DefaultRegistry() (*Registry, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return NewRegistry(filepath.Join(dir, "slack-agent-cli", "policies.json")), nil
}

// Get returns policy state, or an empty identity-bound set.
func (r *Registry) Get(ctx context.Context, p profile.Profile) (Set, error) {
	if err := ctx.Err(); err != nil {
		return Set{}, err
	}
	file, err := r.load()
	if err != nil {
		return Set{}, err
	}
	for _, set := range file.Sets {
		if set.ProfileName == p.Name {
			if err := validateBinding(set, p); err != nil {
				return Set{}, err
			}
			return set, nil
		}
	}
	return newSet(p), nil
}

// Replace atomically replaces one kind while preserving write subset read.
func (r *Registry) Replace(ctx context.Context, p profile.Profile, kind Kind, targets []Target) (Set, error) {
	if len(targets) > contract.MaxPolicyTargets {
		return Set{}, errors.New("policy target count exceeds v1 bound")
	}
	if err := validateTargets(targets); err != nil {
		return Set{}, err
	}
	if err := r.ensureDirectory(); err != nil {
		return Set{}, err
	}
	var result Set
	committed := false
	err := lockfile.With(ctx, r.path+".lock", func() error {
		file, err := r.load()
		if err != nil {
			return err
		}
		index := -1
		for current := range file.Sets {
			if file.Sets[current].ProfileName == p.Name {
				index = current
				break
			}
		}
		set := newSet(p)
		if index >= 0 {
			set = file.Sets[index]
			if err := validateBinding(set, p); err != nil {
				return err
			}
		}
		switch kind {
		case Read:
			set.Reads = cloneTargets(targets)
		case Write:
			set.Writes = cloneTargets(targets)
		default:
			return errors.New("unknown policy kind")
		}
		if !isSubset(set.Writes, set.Reads) {
			return ErrWriteNeedsRead
		}
		if index >= 0 {
			file.Sets[index] = set
		} else {
			file.Sets = append(file.Sets, set)
		}
		if err := r.save(file); err != nil {
			return err
		}
		committed = true
		result = set
		return nil
	})
	if err != nil && committed && !WasCommitted(err) {
		err = &CommitError{cause: err}
	}
	return result, err
}

// PreviewReplace validates the exact local policy transition without writing.
func (r *Registry) PreviewReplace(ctx context.Context, p profile.Profile, kind Kind, targets []Target) (Set, error) {
	if len(targets) > contract.MaxPolicyTargets {
		return Set{}, errors.New("policy target count exceeds v1 bound")
	}
	if err := validateTargets(targets); err != nil {
		return Set{}, err
	}
	set, err := r.Get(ctx, p)
	if err != nil {
		return Set{}, err
	}
	switch kind {
	case Read:
		set.Reads = cloneTargets(targets)
	case Write:
		set.Writes = cloneTargets(targets)
	default:
		return Set{}, errors.New("unknown policy kind")
	}
	if !isSubset(set.Writes, set.Reads) {
		return Set{}, ErrWriteNeedsRead
	}
	return set, nil
}

// PreviewRebindRead resets a stale profile binding locally before replacing its
// complete read target set. The resulting set deliberately drops every write
// target: writes must be re-authorized against the new identity and generation.
func (r *Registry) PreviewRebindRead(ctx context.Context, p profile.Profile, targets []Target) (Set, error) {
	if err := validateReplacement(Read, targets); err != nil {
		return Set{}, err
	}
	file, err := r.load()
	if err != nil {
		return Set{}, err
	}
	for _, set := range file.Sets {
		if set.ProfileName != p.Name {
			continue
		}
		if err := validateBinding(set, p); err == nil {
			return Set{}, ErrRebindNotNeeded
		} else if !errors.Is(err, ErrBindingMismatch) {
			return Set{}, err
		}
		fresh := newSet(p)
		fresh.Reads = cloneTargets(targets)
		return fresh, nil
	}
	return Set{}, ErrRebindNotNeeded
}

// RebindRead atomically replaces a stale profile policy with an exact fresh
// read set. It is intentionally read-only in effect: all old writes are
// discarded, preserving write subset read and requiring fresh authorization.
func (r *Registry) RebindRead(ctx context.Context, p profile.Profile, targets []Target) (Set, error) {
	if err := validateReplacement(Read, targets); err != nil {
		return Set{}, err
	}
	if err := r.ensureDirectory(); err != nil {
		return Set{}, err
	}
	var result Set
	committed := false
	err := lockfile.With(ctx, r.path+".lock", func() error {
		file, err := r.load()
		if err != nil {
			return err
		}
		for index := range file.Sets {
			if file.Sets[index].ProfileName != p.Name {
				continue
			}
			if err := validateBinding(file.Sets[index], p); err == nil {
				return ErrRebindNotNeeded
			} else if !errors.Is(err, ErrBindingMismatch) {
				return err
			}
			fresh := newSet(p)
			fresh.Reads = cloneTargets(targets)
			file.Sets[index] = fresh
			if err := r.save(file); err != nil {
				return err
			}
			committed = true
			result = fresh
			return nil
		}
		return ErrRebindNotNeeded
	})
	if err != nil && committed && !WasCommitted(err) {
		err = &CommitError{cause: err}
	}
	return result, err
}

// Require enforces exact target, binding, and current shared state.
func (r *Registry) Require(ctx context.Context, p profile.Profile, kind Kind, conversationID string, current slack.SharedStatus) error {
	target, err := r.Target(ctx, p, kind, conversationID)
	if err != nil {
		return err
	}
	if target.Shared != current {
		return ErrSharedMismatch
	}
	return nil
}

// Target performs the local-only exact policy check used by dry-run.
func (r *Registry) Target(ctx context.Context, p profile.Profile, kind Kind, conversationID string) (Target, error) {
	set, err := r.Get(ctx, p)
	if err != nil {
		return Target{}, err
	}
	targets := set.Reads
	if kind == Write {
		targets = set.Writes
	}
	for _, target := range targets {
		if target.ConversationID == conversationID {
			return target, nil
		}
	}
	return Target{}, ErrNotAllowed
}

func newSet(p profile.Profile) Set {
	return Set{ProfileName: p.Name, WorkspaceID: p.WorkspaceID, ProfileIdentity: profile.Identity(p), Generation: p.CredentialGeneration}
}
func validateBinding(set Set, p profile.Profile) error {
	if set.WorkspaceID != p.WorkspaceID || set.ProfileIdentity != profile.Identity(p) || set.Generation != p.CredentialGeneration {
		return ErrBindingMismatch
	}
	return nil
}
func validateTargets(targets []Target) error {
	seen := map[string]bool{}
	for _, target := range targets {
		if err := profile.ValidateSlackID(target.ConversationID); err != nil {
			return err
		}
		if seen[target.ConversationID] {
			return errors.New("duplicate policy target")
		}
		seen[target.ConversationID] = true
	}
	return nil
}

func validateReplacement(kind Kind, targets []Target) error {
	if kind != Read {
		return errors.New("stale policy rebind supports read targets only")
	}
	if len(targets) > contract.MaxPolicyTargets {
		return errors.New("policy target count exceeds v1 bound")
	}
	return validateTargets(targets)
}
func cloneTargets(targets []Target) []Target {
	result := append([]Target(nil), targets...)
	sort.Slice(result, func(i, j int) bool { return result[i].ConversationID < result[j].ConversationID })
	return result
}
func isSubset(subset, superset []Target) bool {
	allowed := map[string]slack.SharedStatus{}
	for _, target := range superset {
		allowed[target.ConversationID] = target.Shared
	}
	for _, target := range subset {
		if status, ok := allowed[target.ConversationID]; !ok || status != target.Shared {
			return false
		}
	}
	return true
}

func (r *Registry) ensureDirectory() error {
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("policy directory is insecure")
	}
	return nil
}
func (r *Registry) load() (policyFile, error) {
	if err := r.ensureDirectory(); err != nil {
		return policyFile{}, err
	}
	payload, info, err := safefile.ReadRegular(r.path, contract.MaxRegistryBytes)
	if errors.Is(err, os.ErrNotExist) {
		return policyFile{Version: 1}, nil
	}
	if err != nil {
		return policyFile{}, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return policyFile{}, errors.New("policy registry is insecure")
	}
	var file policyFile
	if err := json.Unmarshal(payload, &file); err != nil {
		return policyFile{}, err
	}
	if file.Version != 1 {
		return policyFile{}, errors.New("unsupported policy version")
	}
	if len(file.Sets) > contract.MaxProfiles {
		return policyFile{}, errors.New("policy profile count exceeds v1 bound")
	}
	seenProfiles := map[string]bool{}
	for _, set := range file.Sets {
		if seenProfiles[set.ProfileName] {
			return policyFile{}, errors.New("duplicate policy profile")
		}
		seenProfiles[set.ProfileName] = true
		if err := profile.ValidateName(set.ProfileName); err != nil {
			return policyFile{}, err
		}
		if err := profile.ValidateSlackID(set.WorkspaceID); err != nil {
			return policyFile{}, err
		}
		if set.ProfileIdentity == "" || set.Generation == "" {
			return policyFile{}, errors.New("policy binding is incomplete")
		}
		if len(set.Reads) > contract.MaxPolicyTargets || len(set.Writes) > contract.MaxPolicyTargets {
			return policyFile{}, errors.New("policy target count exceeds v1 bound")
		}
		if err := validateTargets(set.Reads); err != nil {
			return policyFile{}, err
		}
		if err := validateTargets(set.Writes); err != nil {
			return policyFile{}, err
		}
		if !isSubset(set.Writes, set.Reads) {
			return policyFile{}, ErrWriteNeedsRead
		}
	}
	return file, nil
}
func (r *Registry) save(file policyFile) error {
	sort.Slice(file.Sets, func(i, j int) bool { return file.Sets[i].ProfileName < file.Sets[j].ProfileName })
	payload, err := json.Marshal(file)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(r.path), ".policies-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer func() { _ = os.Remove(name) }()
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
	if err := os.Rename(name, r.path); err != nil {
		return fmt.Errorf("commit policy registry: %w", err)
	}
	dir, err := os.Open(filepath.Dir(r.path))
	if err != nil {
		return &CommitError{cause: fmt.Errorf("open policy registry directory: %w", err)}
	}
	defer func() { _ = dir.Close() }()
	if err := dir.Sync(); err != nil {
		return &CommitError{cause: fmt.Errorf("sync policy registry directory: %w", err)}
	}
	return nil
}
