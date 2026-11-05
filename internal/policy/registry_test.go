package policy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

func testProfile() profile.Profile {
	return profile.Profile{Name: "work", WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com", UserID: "U1", TokenKind: profile.TokenUser, Capabilities: []profile.Capability{profile.CapabilityRead}, CredentialGeneration: "g1"}
}
func TestWriteSubsetReadIsAtomic(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	target := Target{ConversationID: "C1", Shared: slack.SharedStatus{}}
	if _, err := registry.Replace(context.Background(), p, Write, []Target{target}); !errors.Is(err, ErrWriteNeedsRead) {
		t.Fatalf("want subset error, got %v", err)
	}
	if _, err := registry.Replace(context.Background(), p, Read, []Target{target}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Replace(context.Background(), p, Write, []Target{target}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Replace(context.Background(), p, Read, nil); !errors.Is(err, ErrWriteNeedsRead) {
		t.Fatalf("read shrink should fail: %v", err)
	}
	set, err := registry.Get(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reads) != 1 || len(set.Writes) != 1 {
		t.Fatalf("failed mutation changed registry: %+v", set)
	}
}
func TestRequireBindingAndSharedState(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	target := Target{ConversationID: "C1", Shared: slack.SharedStatus{Shared: true, ExternallyShared: true}}
	if _, err := registry.Replace(context.Background(), p, Read, []Target{target}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Require(context.Background(), p, Read, "C1", target.Shared); err != nil {
		t.Fatal(err)
	}
	changed := target.Shared
	changed.ExternallyShared = false
	if err := registry.Require(context.Background(), p, Read, "C1", changed); !errors.Is(err, ErrSharedMismatch) {
		t.Fatalf("want shared mismatch, got %v", err)
	}
	p.CredentialGeneration = "g2"
	if _, err := registry.Get(context.Background(), p); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("want binding mismatch, got %v", err)
	}
}

func TestRebindReadDropsStaleWritesOnlyAfterConfirmedReplacement(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	old := testProfile()
	target := Target{ConversationID: "C1", Shared: slack.SharedStatus{}}
	if _, err := registry.Replace(context.Background(), old, Read, []Target{target}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Replace(context.Background(), old, Write, []Target{target}); err != nil {
		t.Fatal(err)
	}

	fresh := old
	fresh.CredentialGeneration = "g2"
	preview, err := registry.PreviewRebindRead(context.Background(), fresh, []Target{target})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Generation != "g2" || len(preview.Reads) != 1 || len(preview.Writes) != 0 {
		t.Fatalf("unexpected rebind preview: %+v", preview)
	}
	if _, err := registry.Get(context.Background(), old); err != nil {
		t.Fatalf("preview mutated old state: %v", err)
	}
	if _, err := registry.Get(context.Background(), fresh); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("preview changed stale binding: %v", err)
	}

	applied, err := registry.RebindRead(context.Background(), fresh, []Target{target})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Generation != "g2" || len(applied.Reads) != 1 || len(applied.Writes) != 0 {
		t.Fatalf("unexpected rebind result: %+v", applied)
	}
	if _, err := registry.Get(context.Background(), old); !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("old identity was not invalidated: %v", err)
	}
	stored, err := registry.Get(context.Background(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Reads) != 1 || len(stored.Writes) != 0 {
		t.Fatalf("stale write targets survived rebind: %+v", stored)
	}
	if _, err := registry.Replace(context.Background(), fresh, Write, []Target{{ConversationID: "C2"}}); !errors.Is(err, ErrWriteNeedsRead) {
		t.Fatalf("writes before fresh reads were accepted: %v", err)
	}
}

func TestRebindReadRefusesCurrentOrAbsentPolicy(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	target := Target{ConversationID: "C1"}
	if _, err := registry.PreviewRebindRead(context.Background(), p, []Target{target}); !errors.Is(err, ErrRebindNotNeeded) {
		t.Fatalf("absent policy preview=%v", err)
	}
	if _, err := registry.Replace(context.Background(), p, Read, []Target{target}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.RebindRead(context.Background(), p, []Target{target}); !errors.Is(err, ErrRebindNotNeeded) {
		t.Fatalf("current policy rebind=%v", err)
	}
}

func TestConcurrentRebindReturnsStableNoopForSecondReset(t *testing.T) {
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	old := testProfile()
	target := Target{ConversationID: "C1"}
	if _, err := registry.Replace(context.Background(), old, Read, []Target{target}); err != nil {
		t.Fatal(err)
	}
	fresh := old
	fresh.CredentialGeneration = "g2"
	start := make(chan struct{})
	errorsByAttempt := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := registry.RebindRead(context.Background(), fresh, []Target{target})
			errorsByAttempt <- err
		}()
	}
	close(start)
	var successes, noops int
	for range 2 {
		err := <-errorsByAttempt
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRebindNotNeeded):
			noops++
		default:
			t.Fatalf("unexpected concurrent rebind error: %v", err)
		}
	}
	if successes != 1 || noops != 1 {
		t.Fatalf("successes=%d noops=%d", successes, noops)
	}
}

func TestAddMergesReadAndWriteTargetsWithoutAffectingOtherPolicies(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	shared := slack.SharedStatus{Shared: true, ExternallyShared: true}
	if _, err := registry.Replace(context.Background(), p, Read, []Target{{ConversationID: "C2", Shared: shared}, {ConversationID: "C1"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Replace(context.Background(), p, Write, []Target{{ConversationID: "C2", Shared: shared}}); err != nil {
		t.Fatal(err)
	}
	other := p
	other.Name = "personal"
	if _, err := registry.Replace(context.Background(), other, Read, []Target{{ConversationID: "C9"}}); err != nil {
		t.Fatal(err)
	}

	set, err := registry.Add(context.Background(), p, Write, Target{ConversationID: "C3"})
	if err != nil {
		t.Fatal(err)
	}
	set, err = registry.Add(context.Background(), p, Write, Target{ConversationID: "C3"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := set.Reads, []Target{{ConversationID: "C1"}, {ConversationID: "C2", Shared: shared}, {ConversationID: "C3"}}; !equalTargets(got, want) {
		t.Fatalf("reads = %#v, want %#v", got, want)
	}
	if got, want := set.Writes, []Target{{ConversationID: "C2", Shared: shared}, {ConversationID: "C3"}}; !equalTargets(got, want) {
		t.Fatalf("writes = %#v, want %#v", got, want)
	}
	otherSet, err := registry.Get(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := otherSet.Reads, []Target{{ConversationID: "C9"}}; !equalTargets(got, want) || len(otherSet.Writes) != 0 {
		t.Fatalf("other policy changed: %+v", otherSet)
	}
}

func TestAddReadDoesNotGrantWriteAndIsIdempotent(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	target := Target{ConversationID: "C1"}
	if _, err := registry.Add(context.Background(), p, Read, target); err != nil {
		t.Fatal(err)
	}
	set, err := registry.Add(context.Background(), p, Read, target)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := set.Reads, []Target{target}; !equalTargets(got, want) || len(set.Writes) != 0 {
		t.Fatalf("read add granted or duplicated policy: %+v", set)
	}
}

func TestAddConcurrentAppendsPreserveEveryTarget(t *testing.T) {
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	start := make(chan struct{})
	errs := make(chan error, 30)
	var group sync.WaitGroup
	add := func(target Target) {
		defer group.Done()
		<-start
		_, err := registry.Add(context.Background(), p, Read, target)
		errs <- err
	}
	for index := range 25 {
		group.Add(1)
		go add(Target{ConversationID: fmt.Sprintf("C%03d", index)})
	}
	for range 5 {
		group.Add(1)
		go add(Target{ConversationID: "Cshared"})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}
	set, err := registry.Get(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reads) != 26 || len(set.Writes) != 0 {
		t.Fatalf("lost or duplicated concurrent adds: %+v", set)
	}
}

func TestAddRejectsInvalidOrIncompatibleMutationWithoutChanges(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	original := Target{ConversationID: "C1", Shared: slack.SharedStatus{Shared: true}}
	if _, err := registry.Add(context.Background(), p, Read, original); err != nil {
		t.Fatal(err)
	}

	for _, attempt := range []struct {
		name    string
		profile profile.Profile
		kind    Kind
		target  Target
		want    error
	}{
		{name: "invalid kind", profile: p, kind: Kind("other"), target: Target{ConversationID: "C2"}},
		{name: "invalid id", profile: p, kind: Read, target: Target{ConversationID: "C-2"}},
		{name: "shared state mismatch", profile: p, kind: Write, target: Target{ConversationID: "C1"}, want: ErrSharedMismatch},
		{name: "stale binding", profile: func() profile.Profile { updated := p; updated.CredentialGeneration = "g2"; return updated }(), kind: Read, target: Target{ConversationID: "C2"}, want: ErrBindingMismatch},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			_, err := registry.Add(context.Background(), attempt.profile, attempt.kind, attempt.target)
			if attempt.want != nil {
				if !errors.Is(err, attempt.want) {
					t.Fatalf("error = %v, want %v", err, attempt.want)
				}
			} else if err == nil {
				t.Fatal("invalid mutation succeeded")
			}
		})
	}
	set, err := registry.Get(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := set.Reads, []Target{original}; !equalTargets(got, want) || len(set.Writes) != 0 {
		t.Fatalf("failed add changed policy: %+v", set)
	}
}

func TestAddRejectsTargetOverMaximum(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	targets := make([]Target, contract.MaxPolicyTargets)
	for index := range targets {
		targets[index] = Target{ConversationID: fmt.Sprintf("C%03d", index)}
	}
	if _, err := registry.Replace(context.Background(), p, Read, targets); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(context.Background(), p, Read, Target{ConversationID: "Coverflow"}); err == nil {
		t.Fatal("read over maximum succeeded")
	}
	set, err := registry.Get(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reads) != contract.MaxPolicyTargets {
		t.Fatalf("overflow changed policy: %d reads", len(set.Reads))
	}
}

func TestAddRejectsProfileOverMaximumWithoutSavingInvalidRegistry(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	p := testProfile()
	file := policyFile{Version: 1, Sets: make([]Set, contract.MaxProfiles)}
	for index := range file.Sets {
		item := p
		item.Name = fmt.Sprintf("profile-%03d", index)
		file.Sets[index] = newSet(item)
	}
	if err := registry.ensureDirectory(); err != nil {
		t.Fatal(err)
	}
	if err := registry.save(file); err != nil {
		t.Fatal(err)
	}
	p.Name = "overflow"
	if _, err := registry.Add(context.Background(), p, Read, Target{ConversationID: "C1"}); err == nil {
		t.Fatal("profile over maximum succeeded")
	}
	stored, err := registry.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Sets) != contract.MaxProfiles {
		t.Fatalf("overflow changed profile count: %d", len(stored.Sets))
	}
}

func TestAddHonorsCancelledContext(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "policies.json"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.Add(ctx, testProfile(), Read, Target{ConversationID: "C1"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(filepath.Dir(registry.path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled add created policy directory: %v", err)
	}
}

func equalTargets(got, want []Target) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestRegistryRejectsOversizedFileBeforeDecode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "policies.json")
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(path).Get(context.Background(), testProfile()); err == nil {
		t.Fatal("oversized registry was accepted")
	}
}
