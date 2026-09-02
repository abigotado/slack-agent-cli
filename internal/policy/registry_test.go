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
