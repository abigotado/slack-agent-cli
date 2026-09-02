package profile

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func validProfile(name string) Profile {
	return Profile{Name: name, WorkspaceID: "T123", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com", UserID: "U123", TokenKind: TokenUser, Capabilities: []Capability{CapabilityRead}, CredentialGeneration: "generation-1"}
}

func TestProfileValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*Profile)
		valid  bool
	}{{"valid", func(*Profile) {}, true}, {"bad name", func(p *Profile) { p.Name = "bad name" }, false}, {"foreign URL", func(p *Profile) { p.WorkspaceURL = "https://evil.example" }, false}, {"duplicate capability", func(p *Profile) { p.Capabilities = []Capability{CapabilityRead, CapabilityRead} }, false}, {"bot without bot id", func(p *Profile) { p.TokenKind = TokenBot }, false}}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			p := validProfile("work")
			test.mutate(&p)
			if (p.Validate() == nil) != test.valid {
				t.Fatalf("validation mismatch: %+v", p)
			}
		})
	}
}

func TestRegistryRoundTripAndPermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config", "profiles.json")
	registry := NewRegistry(path)
	p := validProfile("work")
	if err := registry.Put(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := registry.Get(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if Identity(got) != Identity(p) {
		t.Fatalf("identity mismatch")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("permissions %o", info.Mode().Perm())
	}
	if err := registry.Remove(context.Background(), "work"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get(context.Background(), "work"); err != ErrNotFound {
		t.Fatalf("want not found, got %v", err)
	}
}

func TestRegistryRejectsSymlink(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte(`{"version":1,"profiles":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "profiles.json")
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(path).List(context.Background()); err == nil {
		t.Fatal("symlink was accepted")
	}
}

func TestRegistryRejectsDuplicateProfileNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	p := validProfile("work")
	payload, err := json.Marshal(registryFile{Version: registryVersion, Profiles: []Profile{p, p}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "profiles.json")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(path).List(context.Background()); err == nil {
		t.Fatal("duplicate profile name was accepted")
	}
}

func TestRegistryRejectsOversizedFileBeforeDecode(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	config := filepath.Join(root, "config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "profiles.json")
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(path).List(context.Background()); err == nil {
		t.Fatal("oversized registry was accepted")
	}
}

func TestRollbackRunsBeforeTransactionUnlock(t *testing.T) {
	t.Parallel()
	registry := NewRegistry(filepath.Join(t.TempDir(), "config", "profiles.json"))
	rollbackStarted := make(chan struct{})
	releaseRollback := make(chan struct{})
	failedDone := make(chan error, 1)
	go func() {
		failedDone <- registry.TransactWithRollback(context.Background(), func(transaction *Transaction) error {
			if err := transaction.Put(validProfile("first")); err != nil {
				return err
			}
			return errors.New("injected pre-commit failure")
		}, func() error {
			close(rollbackStarted)
			<-releaseRollback
			return nil
		})
	}()
	<-rollbackStarted
	putDone := make(chan error, 1)
	go func() { putDone <- registry.Put(context.Background(), validProfile("second")) }()
	select {
	case err := <-putDone:
		t.Fatalf("concurrent transaction bypassed rollback lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseRollback)
	if err := <-failedDone; err == nil {
		t.Fatal("injected failure was lost")
	}
	if err := <-putDone; err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get(context.Background(), "first"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed transaction became visible: %v", err)
	}
}
