package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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
