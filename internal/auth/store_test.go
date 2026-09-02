package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abigotado/slack-agent-cli/internal/profile"
)

func TestReadToken(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input string
		valid       bool
	}{{"valid", "xoxb-token\n", true}, {"empty", "", false}, {"two lines", "one\ntwo\n", false}, {"space", "xoxb bad\n", false}, {"oversized", strings.Repeat("a", 8193), false}}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			got, err := ReadToken(strings.NewReader(test.input))
			if (err == nil) != test.valid {
				t.Fatalf("got %q err %v", got, err)
			}
		})
	}
}

func TestCredentialBinding(t *testing.T) {
	t.Parallel()
	p := profile.Profile{Name: "work", WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com", UserID: "U1", TokenKind: profile.TokenUser, Capabilities: []profile.Capability{profile.CapabilityRead}, CredentialGeneration: "g1"}
	credential := Credential{Version: 1, Token: "sentinel", ProfileIdentity: profile.Identity(p), Generation: "g1", Capabilities: p.Capabilities}
	if err := credential.ValidateBinding(p); err != nil {
		t.Fatal(err)
	}
	p.CredentialGeneration = "g2"
	if err := credential.ValidateBinding(p); !errors.Is(err, ErrBindingMismatch) {
		t.Fatal("stale credential accepted")
	}
	p.CredentialGeneration = "g1"
	p.Capabilities = []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite}
	if err := credential.ValidateBinding(p); !errors.Is(err, ErrBindingMismatch) {
		t.Fatal("capability drift was not classified")
	}
}

func TestCredentialCodec(t *testing.T) {
	t.Parallel()
	credential := Credential{
		Version:         1,
		Token:           "sentinel",
		ProfileIdentity: "identity",
		Generation:      "generation",
		Capabilities:    []profile.Capability{profile.CapabilityRead},
	}
	payload, err := encodeCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCredential(payload)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Version != credential.Version || decoded.Token != credential.Token || decoded.ProfileIdentity != credential.ProfileIdentity || decoded.Generation != credential.Generation || len(decoded.Capabilities) != 1 || decoded.Capabilities[0] != profile.CapabilityRead {
		t.Fatalf("decoded credential does not match input: %#v", decoded)
	}
}

func TestCredentialCodecRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty"},
		{name: "malformed JSON", payload: []byte("{")},
		{name: "missing token", payload: []byte(`{"version":1}`)},
		{name: "unsupported version", payload: []byte(`{"version":2,"token":"sentinel"}`)},
		{name: "oversized", payload: []byte(strings.Repeat("x", 16385))},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := decodeCredential(test.payload); err == nil {
				t.Fatal("invalid credential payload was accepted")
			}
		})
	}
}

func TestEncodeCredentialRejectsInvalidValue(t *testing.T) {
	t.Parallel()
	if _, err := encodeCredential(Credential{Version: 1, ProfileIdentity: "identity", Generation: "generation"}); err == nil {
		t.Fatal("credential without a token was accepted")
	}
}

type memoryStore struct {
	mu            sync.Mutex
	values        map[string]Credential
	saveErr       error
	blockDelete   bool
	deleteStarted chan struct{}
	releaseDelete chan struct{}
}

func (m *memoryStore) Load(_ context.Context, name string) (Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[name]
	if !ok {
		return Credential{}, ErrNotFound
	}
	return value, nil
}
func (m *memoryStore) Save(_ context.Context, name string, value Credential) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.saveErr != nil {
		return m.saveErr
	}
	m.values[name] = value
	return nil
}
func (m *memoryStore) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	if m.blockDelete {
		m.blockDelete = false
		started, release := m.deleteStarted, m.releaseDelete
		m.mu.Unlock()
		close(started)
		<-release
		m.mu.Lock()
	}
	defer m.mu.Unlock()
	delete(m.values, name)
	return nil
}

func TestLoginAndLogoutSerializeCredentialWithProfile(t *testing.T) {
	t.Parallel()
	registry := profile.NewRegistry(filepath.Join(t.TempDir(), "config", "profiles.json"))
	store := &memoryStore{values: map[string]Credential{}}
	verified := profile.Profile{Name: "work", WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com", UserID: "U1", TokenKind: profile.TokenUser, Capabilities: []profile.Capability{profile.CapabilityRead}}
	if _, err := Login(context.Background(), store, registry, verified, "old-token", false); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.blockDelete = true
	store.deleteStarted = make(chan struct{})
	store.releaseDelete = make(chan struct{})
	deleteStarted, releaseDelete := store.deleteStarted, store.releaseDelete
	store.mu.Unlock()

	logoutDone := make(chan error, 1)
	go func() { logoutDone <- Logout(context.Background(), store, registry, "work") }()
	<-deleteStarted
	loginDone := make(chan error, 1)
	go func() {
		_, err := Login(context.Background(), store, registry, verified, "new-token", true)
		loginDone <- err
	}()
	select {
	case err := <-loginDone:
		t.Fatalf("login bypassed the profile transaction lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseDelete)
	if err := <-logoutDone; err != nil {
		t.Fatal(err)
	}
	if err := <-loginDone; err != nil {
		t.Fatal(err)
	}
	p, err := registry.Get(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.Load(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Token != "new-token" || credential.ValidateBinding(p) != nil {
		t.Fatal("concurrent auth operations left profile and credential inconsistent")
	}
}

var _ Store = (*memoryStore)(nil)
var _ = errors.Is
