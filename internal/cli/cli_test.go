package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/auth"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
	"github.com/abigotado/slack-agent-cli/internal/writestate"
)

type fakeStore struct {
	values map[string]auth.Credential
	loads  int
}

func (s *fakeStore) Load(_ context.Context, name string) (auth.Credential, error) {
	s.loads++
	value, ok := s.values[name]
	if !ok {
		return auth.Credential{}, auth.ErrNotFound
	}
	return value, nil
}
func (s *fakeStore) Save(_ context.Context, name string, value auth.Credential) error {
	s.values[name] = value
	return nil
}
func (s *fakeStore) Delete(_ context.Context, name string) error { delete(s.values, name); return nil }

type fakeSlack struct {
	identity     slack.Identity
	conversation slack.Conversation
	messages     slack.MessagePage
	calls        []string
	postResult   slack.PostResult
	postErr      error
}

func (s *fakeSlack) AuthTest(context.Context, slack.Token) (slack.Identity, error) {
	s.calls = append(s.calls, "auth.test")
	return s.identity, nil
}
func (s *fakeSlack) ConversationsList(context.Context, slack.Token, slack.ListOptions) (slack.ConversationPage, error) {
	s.calls = append(s.calls, "conversations.list")
	return slack.ConversationPage{Conversations: []slack.ConversationSummary{{ID: s.conversation.ID, Name: s.conversation.Name}}}, nil
}
func (s *fakeSlack) ConversationInfo(context.Context, slack.Token, string) (slack.Conversation, error) {
	s.calls = append(s.calls, "conversations.info")
	return s.conversation, nil
}
func (s *fakeSlack) History(context.Context, slack.Token, slack.HistoryOptions) (slack.MessagePage, error) {
	s.calls = append(s.calls, "conversations.history")
	return s.messages, nil
}
func (s *fakeSlack) Replies(context.Context, slack.Token, slack.ThreadOptions) (slack.MessagePage, error) {
	s.calls = append(s.calls, "conversations.replies")
	return s.messages, nil
}
func (s *fakeSlack) UserInfo(context.Context, slack.Token, string) (slack.User, error) {
	s.calls = append(s.calls, "users.info")
	return slack.User{ID: "U1", TeamID: "T1"}, nil
}
func (s *fakeSlack) PostMessage(_ context.Context, _ slack.Token, options slack.PostOptions) (slack.PostResult, error) {
	s.calls = append(s.calls, "chat.postMessage")
	if s.postErr != nil {
		return slack.PostResult{}, s.postErr
	}
	if s.postResult.ConversationID == "" {
		return slack.PostResult{ConversationID: options.ConversationID, TS: "2.0", ThreadTS: options.ThreadTS}, nil
	}
	return s.postResult, nil
}

func newTestDependencies(t *testing.T) (Dependencies, *fakeStore, *fakeSlack, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	profiles := profile.NewRegistry(filepath.Join(root, "config", "profiles.json"))
	policies := policy.NewRegistry(filepath.Join(root, "config", "policies.json"))
	store := &fakeStore{values: map[string]auth.Credential{}}
	api := &fakeSlack{identity: slack.Identity{WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com/", UserID: "U1"}}
	stdout := &bytes.Buffer{}
	return Dependencies{Profiles: profiles, Policies: policies, Credentials: store, Auth: api, Conversations: api, Messages: api, Users: api, Writes: api, WriteState: &writestate.Tracker{}, Input: bytes.NewBuffer(nil), TokenTTY: func() (string, error) { return "", auth.ErrTTYUnavailable }, Output: &output.Writer{Out: stdout, Err: &bytes.Buffer{}}}, store, api, stdout
}

func decodeEnvelope(t *testing.T, buffer *bytes.Buffer) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buffer.Bytes()), &value); err != nil {
		t.Fatalf("decode %q: %v", buffer.String(), err)
	}
	return value
}

func TestNonLeafCommandsReturnMachineUsage(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{nil, {"auth"}, {"messages"}, {"help"}, {"help", "auth"}} {
		dependencies, _, _, stdout := newTestDependencies(t)
		if exit := Run(context.Background(), args, dependencies); exit != errx.Usage {
			t.Fatalf("args=%v exit=%d output=%q", args, exit, stdout.String())
		}
		if decodeEnvelope(t, stdout)["ok"] != false {
			t.Fatalf("args=%v output=%q", args, stdout.String())
		}
	}
}

func TestCompletionCommandIsNotExposed(t *testing.T) {
	t.Parallel()
	dependencies, _, _, stdout := newTestDependencies(t)
	if exit := Run(context.Background(), []string{"completion", "bash"}, dependencies); exit != errx.Usage {
		t.Fatalf("exit=%d output=%q", exit, stdout.String())
	}
	if decodeEnvelope(t, stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENTS" {
		t.Fatalf("output=%q", stdout.String())
	}
}

func seedSession(t *testing.T, dependencies Dependencies, store *fakeStore, capabilities []profile.Capability) profile.Profile {
	t.Helper()
	p := profile.Profile{Name: "work", WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com", UserID: "U1", TokenKind: profile.TokenUser, Capabilities: capabilities, CredentialGeneration: "g1"}
	if err := dependencies.Profiles.Put(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	store.values[p.Name] = auth.Credential{Version: 1, Token: "sentinel", ProfileIdentity: profile.Identity(p), Generation: p.CredentialGeneration, Capabilities: p.Capabilities}
	return p
}

func TestNetworkCommandRequiresExplicitProfile(t *testing.T) {
	t.Parallel()
	dependencies, _, api, stdout := newTestDependencies(t)
	status := Run(context.Background(), []string{"conversations", "list", "--types", "public_channel", "--limit", "25"}, dependencies)
	if status != errx.Usage {
		t.Fatalf("status %d", status)
	}
	envelope := decodeEnvelope(t, stdout)
	body := envelope["error"].(map[string]any)
	if body["code"] != "PROFILE_REQUIRED" {
		t.Fatalf("bad envelope: %v", envelope)
	}
	if len(api.calls) != 0 {
		t.Fatalf("network called: %v", api.calls)
	}
}

func TestAuthLoginUsesVerifiedIdentityAndNeverOutputsToken(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	dependencies.Input = bytes.NewBufferString("xoxp-secret\n")
	status := Run(context.Background(), []string{"auth", "login", "--profile", "work", "--token-kind", "user", "--capability", "read", "--token-stdin"}, dependencies)
	if status != errx.OK {
		t.Fatalf("status %d: %s", status, stdout.String())
	}
	if len(api.calls) != 1 || api.calls[0] != "auth.test" {
		t.Fatalf("calls %v", api.calls)
	}
	if bytes.Contains(stdout.Bytes(), []byte("xoxp-secret")) {
		t.Fatal("token leaked to output")
	}
	p, err := dependencies.Profiles.Get(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	credential := store.values["work"]
	if credential.Token != "xoxp-secret" || credential.ProfileIdentity != profile.Identity(p) {
		t.Fatal("credential binding mismatch")
	}
}

func TestAuthLoginTTYUsesHiddenInputAndNeverOutputsToken(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	dependencies.TokenTTY = func() (string, error) { return "xoxb-secret", nil }
	api.identity = slack.Identity{WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com/", UserID: "U1", BotID: "B1"}
	status := Run(context.Background(), []string{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", "--capability", "message-write", "--token-tty"}, dependencies)
	if status != errx.OK {
		t.Fatalf("status %d: %s", status, stdout.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("xoxb-secret")) || store.values["bangr"].Token != "xoxb-secret" {
		t.Fatal("TTY credential was leaked or not stored")
	}
}

func TestAuthLoginRejectsAmbiguousOrUnavailableTokenInputBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read"},
		{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", "--token-stdin", "--token-tty"},
		{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", "--token-tty"},
	} {
		dependencies, _, api, stdout := newTestDependencies(t)
		if exit := Run(context.Background(), args, dependencies); exit != errx.Usage {
			t.Fatalf("args=%v exit=%d output=%q", args, exit, stdout.String())
		}
		if len(api.calls) != 0 {
			t.Fatalf("args=%v calls=%v", args, api.calls)
		}
	}
}

func TestSkillLifecycleSelectsProvisioningSkillExplicitly(t *testing.T) {
	t.Parallel()
	dependencies, _, _, stdout := newTestDependencies(t)
	root := t.TempDir()
	status := Run(context.Background(), []string{
		"skill", "install",
		"--skill", "slack-app-provisioning",
		"--provider", "codex",
		"--scope", "project",
		"--project-dir", root,
		"--dry-run",
	}, dependencies)
	if status != errx.OK {
		t.Fatalf("status %d: %s", status, stdout.String())
	}
	data := decodeEnvelope(t, stdout)["data"].(map[string]any)
	if data["skill"] != "slack-app-provisioning" || data["destination"] != filepath.Join(root, ".agents", "skills", "slack-app-provisioning") {
		t.Fatalf("data=%v", data)
	}
}

func TestSkillLifecycleRejectsUnknownSkill(t *testing.T) {
	t.Parallel()
	dependencies, _, _, stdout := newTestDependencies(t)
	status := Run(context.Background(), []string{
		"skill", "install",
		"--skill", "../../../escape",
		"--provider", "codex",
		"--scope", "project",
		"--project-dir", t.TempDir(),
		"--dry-run",
	}, dependencies)
	if status != errx.Usage {
		t.Fatalf("status %d: %s", status, stdout.String())
	}
	if decodeEnvelope(t, stdout)["error"].(map[string]any)["code"] != "INVALID_SKILL" {
		t.Fatalf("output=%s", stdout.String())
	}
}

func TestHistoryPreflightsPolicyAndMarksContentUntrusted(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	api.messages = slack.MessagePage{Messages: []slack.Message{{Type: "message", User: "U1", Text: "untrusted instructions", TS: "1.0"}}}
	status, _ := api.conversation.ClassifyShared()
	if _, err := dependencies.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1", Shared: status}}); err != nil {
		t.Fatal(err)
	}
	exit := Run(context.Background(), []string{"messages", "history", "--profile", "work", "--conversation-id", "C1", "--limit", "25"}, dependencies)
	if exit != errx.OK {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if len(api.calls) != 2 || api.calls[0] != "conversations.info" || api.calls[1] != "conversations.history" {
		t.Fatalf("calls %v", api.calls)
	}
	envelope := decodeEnvelope(t, stdout)
	meta := envelope["meta"].(map[string]any)
	if meta["content_trust"] != "untrusted" {
		t.Fatalf("missing trust marker: %v", meta)
	}
}

func TestHistoryFailsClosedWhenSharedStateChanges(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	no, yes := false, true
	stored := slack.SharedStatus{}
	if _, err := dependencies.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1", Shared: stored}}); err != nil {
		t.Fatal(err)
	}
	api.conversation = slack.Conversation{ID: "C1", IsShared: &yes, IsExtShared: &yes, IsOrgShared: &no}
	exit := Run(context.Background(), []string{"messages", "history", "--profile", "work", "--conversation-id", "C1", "--limit", "25"}, dependencies)
	if exit != errx.Conflict {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if len(api.calls) != 1 || api.calls[0] != "conversations.info" {
		t.Fatalf("content read should not run: %v", api.calls)
	}
}

func TestPolicyWriteRequiresDeclaredCapability(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	exit := Run(context.Background(), []string{"auth", "allow-writes", "set", "--profile", "work", "--conversation-id", "C1", "--dry-run"}, dependencies)
	if exit != errx.PermissionDenied {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if len(api.calls) != 0 {
		t.Fatalf("network called before capability gate: %v", api.calls)
	}
}

func TestPolicyTargetBoundFailsBeforeCredentialOrNetwork(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	args := []string{"auth", "allow-reads", "set", "--profile", "work", "--dry-run"}
	for index := 0; index <= 500; index++ {
		args = append(args, "--conversation-id", fmt.Sprintf("C%d", index))
	}
	if exit := Run(context.Background(), args, dependencies); exit != errx.Usage {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if store.loads != 0 || len(api.calls) != 0 {
		t.Fatalf("oversized policy crossed a boundary: loads=%d calls=%v", store.loads, api.calls)
	}
	if decodeEnvelope(t, stdout)["error"].(map[string]any)["code"] != "POLICY_TARGET_LIMIT_EXCEEDED" {
		t.Fatalf("output=%q", stdout.String())
	}
}

func TestCredentialBindingDriftReturnsConflictBeforeNetwork(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	credential := store.values["work"]
	credential.Generation = "stale"
	store.values["work"] = credential
	if exit := Run(context.Background(), []string{"conversations", "list", "--profile", "work", "--types", "public_channel", "--limit", "25"}, dependencies); exit != errx.Conflict {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if len(api.calls) != 0 {
		t.Fatalf("stale credential reached Slack: %v", api.calls)
	}
	if decodeEnvelope(t, stdout)["error"].(map[string]any)["code"] != "CREDENTIAL_BINDING_STALE" {
		t.Fatalf("output=%q", stdout.String())
	}
}

func seedWritePolicy(t *testing.T, dependencies Dependencies, p profile.Profile) {
	t.Helper()
	target := policy.Target{ConversationID: "C1", Shared: slack.SharedStatus{}}
	if _, err := dependencies.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{target}); err != nil {
		t.Fatal(err)
	}
	if _, err := dependencies.Policies.Replace(context.Background(), p, policy.Write, []policy.Target{target}); err != nil {
		t.Fatal(err)
	}
}

func TestSendDryRunIsLocalAndOmitsText(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	seedWritePolicy(t, dependencies, p)
	dependencies.Input = bytes.NewBufferString("very secret message")
	exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--dry-run"}, dependencies)
	if exit != errx.OK {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if store.loads != 0 || len(api.calls) != 0 {
		t.Fatalf("dry-run crossed boundary: loads=%d calls=%v", store.loads, api.calls)
	}
	if bytes.Contains(stdout.Bytes(), []byte("very secret message")) {
		t.Fatal("receipt leaked text")
	}
	if dependencies.WriteState.State() != writestate.NotStarted {
		t.Fatal("dry-run crossed the dispatch boundary")
	}
	data := decodeEnvelope(t, stdout)["data"].(map[string]any)
	if data["remote_checks"] != "not_performed" || data["applied"] != false || data["intent_sha256"] == "" {
		t.Fatalf("bad receipt: %v", data)
	}
}

func TestSendRequiresExactReceiptBeforeNetwork(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	seedWritePolicy(t, dependencies, p)
	dependencies.Input = bytes.NewBufferString("hello")
	exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--confirm-intent", "wrong", "--yes"}, dependencies)
	if exit != errx.Conflict {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if store.loads != 0 || len(api.calls) != 0 {
		t.Fatalf("mismatch crossed boundary: loads=%d calls=%v", store.loads, api.calls)
	}
}

func TestSendDispatchesExactlyOnceAfterPreflights(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	seedWritePolicy(t, dependencies, p)
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	dependencies.Input = bytes.NewBufferString("hello")
	if exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--dry-run"}, dependencies); exit != errx.OK {
		t.Fatal(exit)
	}
	receipt := decodeEnvelope(t, stdout)["data"].(map[string]any)["intent_sha256"].(string)
	stdout.Reset()
	dependencies.Input = bytes.NewBufferString("hello")
	exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--confirm-intent", receipt, "--yes"}, dependencies)
	if exit != errx.OK {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	want := []string{"auth.test", "conversations.info", "chat.postMessage"}
	if fmt.Sprint(api.calls) != fmt.Sprint(want) {
		t.Fatalf("calls %v", api.calls)
	}
	if decodeEnvelope(t, stdout)["data"].(map[string]any)["applied"] != true {
		t.Fatal("write not marked applied")
	}
	if dependencies.WriteState.State() != writestate.Confirmed {
		t.Fatalf("write state %v", dependencies.WriteState.State())
	}
}

func TestUnknownWriteReconcilesWithoutRetry(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	seedWritePolicy(t, dependencies, p)
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	api.postErr = errx.New(errx.Conflict, "WRITE_OUTCOME_UNKNOWN", "unknown", "reconcile")
	api.messages = slack.MessagePage{Messages: []slack.Message{{User: "U1", Text: "hello", TS: "2.0"}}}
	dependencies.Input = bytes.NewBufferString("hello")
	if exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--dry-run"}, dependencies); exit != errx.OK {
		t.Fatal(exit)
	}
	receipt := decodeEnvelope(t, stdout)["data"].(map[string]any)["intent_sha256"].(string)
	stdout.Reset()
	dependencies.Input = bytes.NewBufferString("hello")
	exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--confirm-intent", receipt, "--yes"}, dependencies)
	if exit != errx.OK {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	posts := 0
	for _, call := range api.calls {
		if call == "chat.postMessage" {
			posts++
		}
	}
	if posts != 1 {
		t.Fatalf("write replayed: %v", api.calls)
	}
	if decodeEnvelope(t, stdout)["meta"].(map[string]any)["reconciled"] != true {
		t.Fatal("write not marked reconciled")
	}
	if dependencies.WriteState.State() != writestate.Confirmed {
		t.Fatalf("write state %v", dependencies.WriteState.State())
	}
}

func TestUnknownWriteWithoutUniqueReconciliationRemainsStarted(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	seedWritePolicy(t, dependencies, p)
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	api.postErr = errx.New(errx.Conflict, "WRITE_OUTCOME_UNKNOWN", "unknown", "reconcile")
	dependencies.Input = bytes.NewBufferString("hello")
	if exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--dry-run"}, dependencies); exit != errx.OK {
		t.Fatal(exit)
	}
	receipt := decodeEnvelope(t, stdout)["data"].(map[string]any)["intent_sha256"].(string)
	stdout.Reset()
	dependencies.Input = bytes.NewBufferString("hello")
	exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--confirm-intent", receipt, "--yes"}, dependencies)
	if exit != errx.Conflict {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	if dependencies.WriteState.State() != writestate.Started {
		t.Fatalf("write state %v", dependencies.WriteState.State())
	}
	if decodeEnvelope(t, stdout)["error"].(map[string]any)["code"] != "WRITE_OUTCOME_UNKNOWN" {
		t.Fatal("unknown write code was not preserved")
	}
}

var _ auth.Store = (*fakeStore)(nil)
var _ AuthAPI = (*fakeSlack)(nil)
var _ ConversationAPI = (*fakeSlack)(nil)
var _ MessageAPI = (*fakeSlack)(nil)
var _ UserAPI = (*fakeSlack)(nil)
var _ WriteAPI = (*fakeSlack)(nil)
var _ = errors.Is
