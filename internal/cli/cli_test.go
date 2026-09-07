package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
	historyOptions []slack.HistoryOptions
	replyOptions   []slack.ThreadOptions
	replyPages     []slack.MessagePage
	identity       slack.Identity
	conversation   slack.Conversation
	messages       slack.MessagePage
	user           slack.User
	calls          []string
	postResult     slack.PostResult
	postErr        error
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
func (s *fakeSlack) History(_ context.Context, _ slack.Token, options slack.HistoryOptions) (slack.MessagePage, error) {
	s.historyOptions = append(s.historyOptions, options)
	s.calls = append(s.calls, "conversations.history")
	return s.messages, nil
}
func (s *fakeSlack) Replies(_ context.Context, _ slack.Token, options slack.ThreadOptions) (slack.MessagePage, error) {
	s.replyOptions = append(s.replyOptions, options)
	s.calls = append(s.calls, "conversations.replies")
	if len(s.replyPages) > 0 {
		page := s.replyPages[0]
		s.replyPages = s.replyPages[1:]
		return page, nil
	}
	return s.messages, nil
}
func (s *fakeSlack) UserInfo(_ context.Context, _ slack.Token, id string) (slack.User, error) {
	s.calls = append(s.calls, "users.info")
	if s.user.ID != "" || s.user.TeamID != "" {
		return s.user, nil
	}
	return slack.User{ID: id, TeamID: "T1"}, nil
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
	return Dependencies{Profiles: profiles, Policies: policies, Credentials: store, Auth: api, Conversations: api, Messages: api, Users: api, Files: &fakeFiles{}, Writes: api, WriteState: &writestate.Tracker{}, Input: bytes.NewBuffer(nil), TokenTTY: func() (string, error) { return "", auth.ErrTTYUnavailable }, Output: &output.Writer{Out: stdout, Err: &bytes.Buffer{}}}, store, api, stdout
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
	for _, args := range [][]string{nil, {"auth"}, {"messages"}, {"files"}, {"help"}, {"help", "auth"}} {
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
	for _, args := range [][]string{
		{"conversations", "list", "--types", "public_channel", "--limit", "25"},
		{"files", "get", "F1", "--conversation-id", "C1", "--message-ts", "1.0"},
		{"files", "download", "F1", "--conversation-id", "C1", "--message-ts", "1.0", "--output", "video.mp4"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			dependencies, store, api, stdout := newTestDependencies(t)
			if status := Run(context.Background(), args, dependencies); status != errx.Usage {
				t.Fatalf("status %d", status)
			}
			envelope := decodeEnvelope(t, stdout)
			if envelope["error"].(map[string]any)["code"] != "PROFILE_REQUIRED" {
				t.Fatalf("bad envelope: %v", envelope)
			}
			if len(api.calls) != 0 || len(dependencies.Files.(*fakeFiles).calls) != 0 || store.loads != 0 {
				t.Fatal("missing profile reached credentials or network")
			}
		})
	}
}

func TestAuthLoginUsesVerifiedIdentityAndNeverOutputsToken(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	token := "xox" + "p-secret"
	dependencies.Input = bytes.NewBufferString(token + "\n")
	status := Run(context.Background(), []string{"auth", "login", "--profile", "work", "--token-kind", "user", "--capability", "read", "--token-stdin"}, dependencies)
	if status != errx.OK {
		t.Fatalf("status %d: %s", status, stdout.String())
	}
	if len(api.calls) != 1 || api.calls[0] != "auth.test" {
		t.Fatalf("calls %v", api.calls)
	}
	if bytes.Contains(stdout.Bytes(), []byte(token)) {
		t.Fatal("token leaked to output")
	}
	p, err := dependencies.Profiles.Get(context.Background(), "work")
	if err != nil {
		t.Fatal(err)
	}
	credential := store.values["work"]
	if credential.Token != token || credential.ProfileIdentity != profile.Identity(p) {
		t.Fatal("credential binding mismatch")
	}
}

func TestAuthLoginTTYUsesHiddenInputAndNeverOutputsToken(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	dependencies.TokenTTY = func() (string, error) { return "credential-sentinel", nil }
	api.identity = slack.Identity{WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com/", UserID: "U1", BotID: "B1"}
	status := Run(context.Background(), []string{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", "--capability", "message-write", "--token-tty"}, dependencies)
	if status != errx.OK {
		t.Fatalf("status %d: %s", status, stdout.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("credential-sentinel")) || store.values["bangr"].Token != "credential-sentinel" {
		t.Fatal("TTY credential was leaked or not stored")
	}
}

func TestAuthLoginRejectsInvalidTokenPayloadBeforeNetwork(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		selector  string
		configure func(*Dependencies)
	}{
		{"stdin", "--token-stdin", func(dependencies *Dependencies) {
			dependencies.Input = bytes.NewBufferString("xox" + "b-\x7f\n")
		}},
		{"TTY", "--token-tty", func(dependencies *Dependencies) {
			dependencies.TokenTTY = func() (string, error) { return "xox" + "b-\u200b", nil }
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies, _, api, stdout := newTestDependencies(t)
			test.configure(&dependencies)
			args := []string{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", test.selector}
			if exit := Run(context.Background(), args, dependencies); exit != errx.Usage {
				t.Fatalf("exit=%d output=%q", exit, stdout.String())
			}
			if len(api.calls) != 0 {
				t.Fatalf("network called: %v", api.calls)
			}
			if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "INVALID_TOKEN_INPUT" {
				t.Fatalf("code=%v", code)
			}
		})
	}
}

func TestAuthLoginRejectsAmbiguousOrUnavailableTokenInputBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		args []string
		code string
	}{
		{[]string{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read"}, "TOKEN_STDIN_REQUIRED"},
		{[]string{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", "--token-stdin", "--token-tty"}, "TOKEN_INPUT_CONFLICT"},
		{[]string{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", "--token-tty"}, "TOKEN_TTY_UNAVAILABLE"},
	} {
		dependencies, _, api, stdout := newTestDependencies(t)
		if exit := Run(context.Background(), test.args, dependencies); exit != errx.Usage {
			t.Fatalf("args=%v exit=%d output=%q", test.args, exit, stdout.String())
		}
		if len(api.calls) != 0 {
			t.Fatalf("args=%v calls=%v", test.args, api.calls)
		}
		if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != test.code {
			t.Fatalf("args=%v code=%v want=%s", test.args, code, test.code)
		}
		if test.code == "TOKEN_STDIN_REQUIRED" {
			errorBody := decodeEnvelope(t, stdout)["error"].(map[string]any)
			if errorBody["message"] != "--token-stdin is required" {
				t.Fatalf("legacy error body=%v", errorBody)
			}
		}
	}
}

func TestAuthLoginClassifiesTTYRecoveryFailuresBeforeNetwork(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		err  error
		exit errx.Exit
		code string
	}{
		{auth.ErrTTYInterrupted, errx.Usage, "TOKEN_TTY_INTERRUPTED"},
		{auth.ErrTTYRestore, errx.Internal, "TOKEN_TTY_RECOVERY_REQUIRED"},
		{auth.ErrTTYIO, errx.Internal, "TOKEN_TTY_IO_FAILED"},
	} {
		dependencies, _, api, stdout := newTestDependencies(t)
		dependencies.TokenTTY = func() (string, error) { return "", test.err }
		args := []string{"auth", "login", "--profile", "bangr", "--token-kind", "bot", "--capability", "read", "--token-tty"}
		if exit := Run(context.Background(), args, dependencies); exit != test.exit {
			t.Fatalf("err=%v exit=%d output=%q", test.err, exit, stdout.String())
		}
		if len(api.calls) != 0 {
			t.Fatalf("err=%v calls=%v", test.err, api.calls)
		}
		if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != test.code {
			t.Fatalf("err=%v code=%v want=%s", test.err, code, test.code)
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

func TestUserProfileReadsAllowlistedDirectMessage(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	no := false
	api.conversation = slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsPrivate: true, IsOrgShared: &no}
	api.messages = slack.MessagePage{Messages: []slack.Message{{Type: "message", User: "U2", Text: "untrusted direct message", TS: "1.0"}}}
	if _, err := dependencies.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "D1", Shared: slack.SharedStatus{}}}); err != nil {
		t.Fatal(err)
	}
	exit := Run(context.Background(), []string{"messages", "history", "--profile", "work", "--conversation-id", "D1", "--limit", "25"}, dependencies)
	if exit != errx.OK {
		t.Fatalf("exit %d: %s", exit, stdout.String())
	}
	wantCalls := []string{"conversations.info", "users.info", "conversations.history"}
	if fmt.Sprint(api.calls) != fmt.Sprint(wantCalls) {
		t.Fatalf("calls %v", api.calls)
	}
	meta := decodeEnvelope(t, stdout)["meta"].(map[string]any)
	if meta["content_trust"] != "untrusted" {
		t.Fatalf("missing trust marker: %v", meta)
	}
}

func TestUsersGetOmitsInternalStrangerClassification(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	api.user = slack.User{ID: "U2", TeamID: "T1", IsStranger: true}
	if exit := Run(context.Background(), []string{"users", "get", "U2", "--profile", "work"}, dependencies); exit != errx.OK {
		t.Fatalf("exit=%d output=%s", exit, stdout.String())
	}
	data := decodeEnvelope(t, stdout)["data"].(map[string]any)
	if _, found := data["is_stranger"]; found {
		t.Fatalf("internal stranger classification leaked: %v", data)
	}
}

func TestCredentialChecksRejectEnterpriseIdentityDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "auth status", args: []string{"auth", "status", "--profile", "work", "--check"}},
		{name: "me", args: []string{"me", "--profile", "work"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies, store, api, stdout := newTestDependencies(t)
			seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
			api.identity.EnterpriseID = "E1"
			if exit := Run(context.Background(), test.args, dependencies); exit != errx.Conflict {
				t.Fatalf("exit=%d output=%s", exit, stdout.String())
			}
			if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "CREDENTIAL_IDENTITY_CHANGED" {
				t.Fatalf("code=%v", code)
			}
			if fmt.Sprint(api.calls) != fmt.Sprint([]string{"auth.test"}) {
				t.Fatalf("calls=%v", api.calls)
			}
		})
	}
}

func TestDirectMessageClassificationValidatesShapeAndParticipant(t *testing.T) {
	t.Parallel()
	no, yes := false, true
	tests := []struct {
		name                 string
		conversation         slack.Conversation
		user                 slack.User
		enterpriseID         string
		identityEnterpriseID string
		want                 slack.SharedStatus
		wantCode             string
		wantError            bool
		wantUserCall         bool
	}{
		{
			name:         "same workspace",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &no},
			user:         slack.User{ID: "U2", TeamID: "T1"},
			wantUserCall: true,
		},
		{
			name:         "different workspace is conservatively external",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &no},
			user:         slack.User{ID: "U2", TeamID: "T2"},
			want:         slack.SharedStatus{Shared: true, ExternallyShared: true},
			wantUserCall: true,
		},
		{
			name:         "malformed participant",
			conversation: slack.Conversation{ID: "D1", User: "U!", IsIM: true, IsOrgShared: &no},
			wantError:    true,
		},
		{
			name:         "over-bound participant",
			conversation: slack.Conversation{ID: "D1", User: strings.Repeat("U", 65), IsIM: true, IsOrgShared: &no},
			wantError:    true,
		},
		{
			name:         "mismatched returned participant",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &no},
			user:         slack.User{ID: "U3", TeamID: "T1"},
			wantError:    true,
			wantUserCall: true,
		},
		{
			name:         "malformed returned participant",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &no},
			user:         slack.User{ID: "U!", TeamID: "T1"},
			wantError:    true,
			wantUserCall: true,
		},
		{
			name:         "malformed returned team",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &no},
			user:         slack.User{ID: "U2", TeamID: "T!"},
			wantError:    true,
			wantUserCall: true,
		},
		{
			name:         "missing returned team",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &no},
			user:         slack.User{ID: "U2"},
			wantError:    true,
			wantUserCall: true,
		},
		{
			name:         "contradictory channel flag",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsChannel: true, IsOrgShared: &no},
			wantError:    true,
		},
		{
			name:         "contradictory mpim flag",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsMPIM: true, IsOrgShared: &no},
			wantError:    true,
		},
		{
			name:         "non-D IM",
			conversation: slack.Conversation{ID: "C1", User: "U2", IsIM: true, IsOrgShared: &no},
			wantError:    true,
		},
		{
			name:         "standalone user allows missing org classification after participant verification",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true},
			user:         slack.User{ID: "U2", TeamID: "T1"},
			wantUserCall: true,
		},
		{
			name:         "missing org classification with stranger participant is external",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true},
			user:         slack.User{ID: "U2", TeamID: "T1", IsStranger: true},
			want:         slack.SharedStatus{Shared: true, ExternallyShared: true},
			wantUserCall: true,
		},
		{
			name:         "known organization sharing is preserved",
			conversation: slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &yes},
			user:         slack.User{ID: "U2", TeamID: "T1"},
			want:         slack.SharedStatus{Shared: true, OrgShared: true},
			wantUserCall: true,
		},
		{
			name:                 "missing org classification fails for enterprise profile",
			conversation:         slack.Conversation{ID: "D1", User: "U2", IsIM: true},
			user:                 slack.User{ID: "U2", TeamID: "T1"},
			enterpriseID:         "E1",
			identityEnterpriseID: "E1",
			wantCode:             "TARGET_SHARED_STATE_UNKNOWN",
			wantError:            true,
			wantUserCall:         true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies, store, api, _ := newTestDependencies(t)
			p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
			p.EnterpriseID = test.enterpriseID
			api.identity.EnterpriseID = test.identityEnterpriseID
			api.user = test.user
			got, err := classifyTarget(context.Background(), dependencies, session{profile: p, token: "sentinel"}, test.conversation)
			if (err != nil) != test.wantError {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if err == nil && got != test.want {
				t.Fatalf("result=%+v want=%+v", got, test.want)
			}
			if test.wantCode != "" && errx.As(err).Code != test.wantCode {
				t.Fatalf("code=%s err=%v", errx.As(err).Code, err)
			}
			userCalled := slices.Contains(api.calls, "users.info")
			if userCalled != test.wantUserCall {
				t.Fatalf("calls=%v", api.calls)
			}
		})
	}
}

func TestNonDirectTargetsStillRequireCompleteSharedState(t *testing.T) {
	t.Parallel()
	no := false
	tests := []struct {
		name         string
		conversation slack.Conversation
		wantError    bool
	}{
		{name: "incomplete channel", conversation: slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsOrgShared: &no}, wantError: true},
		{name: "incomplete mpim", conversation: slack.Conversation{ID: "G1", IsMPIM: true, IsPrivate: true, IsShared: &no, IsOrgShared: &no}, wantError: true},
		{name: "complete mpim", conversation: slack.Conversation{ID: "G1", IsMPIM: true, IsPrivate: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			dependencies, store, api, _ := newTestDependencies(t)
			p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
			_, err := classifyTarget(context.Background(), dependencies, session{profile: p, token: "sentinel"}, test.conversation)
			if (err != nil) != test.wantError {
				t.Fatalf("target=%+v err=%v", test.conversation, err)
			}
			if slices.Contains(api.calls, "users.info") {
				t.Fatalf("non-DM target caused user lookup: %+v", test.conversation)
			}
		})
	}
}

func TestDirectMessageFromDifferentWorkspaceRequiresSlackConnectOptIn(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	no := false
	api.conversation = slack.Conversation{ID: "D1", User: "U2", IsIM: true, IsOrgShared: &no}
	api.user = slack.User{ID: "U2", TeamID: "T2"}
	args := []string{"auth", "allow-reads", "set", "--profile", "work", "--conversation-id", "D1", "--dry-run"}
	if exit := Run(context.Background(), args, dependencies); exit != errx.PermissionDenied {
		t.Fatalf("exit=%d output=%s", exit, stdout.String())
	}
	if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "SLACK_CONNECT_OPT_IN_REQUIRED" {
		t.Fatalf("code=%v", code)
	}

	api.calls = nil
	stdout.Reset()
	args = append(args, "--allow-slack-connect")
	if exit := Run(context.Background(), args, dependencies); exit != errx.OK {
		t.Fatalf("opt-in exit=%d output=%s", exit, stdout.String())
	}
	if fmt.Sprint(api.calls) != fmt.Sprint([]string{"conversations.info", "users.info"}) {
		t.Fatalf("calls=%v", api.calls)
	}
}

func TestDirectMessageParticipantWorkspaceIsRevalidatedBeforeContent(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	api.conversation = slack.Conversation{ID: "D1", User: "U2", IsIM: true}
	api.user = slack.User{ID: "U2", TeamID: "T1"}
	if exit := Run(context.Background(), []string{"auth", "allow-reads", "set", "--profile", "work", "--conversation-id", "D1", "--yes"}, dependencies); exit != errx.OK {
		t.Fatalf("policy exit %d: %s", exit, stdout.String())
	}
	if fmt.Sprint(api.calls) != fmt.Sprint([]string{"conversations.info", "users.info", "auth.test"}) {
		t.Fatalf("policy calls=%v", api.calls)
	}

	api.user.TeamID = "T2"
	for _, args := range [][]string{
		{"messages", "history", "--profile", "work", "--conversation-id", "D1", "--limit", "25"},
		{"messages", "thread", "--profile", "work", "--conversation-id", "D1", "--thread-ts", "1.0", "--limit", "25"},
	} {
		api.calls = nil
		stdout.Reset()
		if exit := Run(context.Background(), args, dependencies); exit != errx.Conflict {
			t.Fatalf("args=%v exit=%d output=%s", args, exit, stdout.String())
		}
		if fmt.Sprint(api.calls) != fmt.Sprint([]string{"conversations.info", "users.info"}) {
			t.Fatalf("args=%v calls=%v", args, api.calls)
		}
		if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "TARGET_SHARED_STATE_CHANGED" {
			t.Fatalf("args=%v code=%v", args, code)
		}
	}

	api.calls = nil
	stdout.Reset()
	dependencies.Input = bytes.NewBufferString("not sent")
	if exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "D1", "--text-stdin", "--dry-run"}, dependencies); exit != errx.PermissionDenied {
		t.Fatalf("send exit=%d output=%s", exit, stdout.String())
	}
	if len(api.calls) != 0 {
		t.Fatalf("read-only user profile reached Slack for send: %v", api.calls)
	}
}

func TestReadPolicyResetRebindsNewProfileAndDropsOldWrites(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	old := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	seedWritePolicy(t, dependencies, old)
	fresh := old
	fresh.CredentialGeneration = "g2"
	if err := dependencies.Profiles.Put(context.Background(), fresh); err != nil {
		t.Fatal(err)
	}
	store.values[fresh.Name] = auth.Credential{Version: 1, Token: "sentinel", ProfileIdentity: profile.Identity(fresh), Generation: fresh.CredentialGeneration, Capabilities: fresh.Capabilities}
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	args := []string{"auth", "allow-reads", "set", "--profile", "work", "--conversation-id", "C1", "--reset-stale-policy", "--dry-run"}
	if exit := Run(context.Background(), args, dependencies); exit != errx.OK {
		t.Fatalf("preview exit=%d output=%s", exit, stdout.String())
	}
	preview := decodeEnvelope(t, stdout)["data"].(map[string]any)["policy"].(map[string]any)
	if preview["generation"] != "g2" || preview["writes"] != nil {
		t.Fatalf("preview did not drop stale writes: %v", preview)
	}
	if _, err := dependencies.Policies.Get(context.Background(), old); err != nil {
		t.Fatalf("dry-run mutated old policy: %v", err)
	}
	if _, err := dependencies.Policies.Get(context.Background(), fresh); !errors.Is(err, policy.ErrBindingMismatch) {
		t.Fatalf("dry-run rebound policy: %v", err)
	}

	stdout.Reset()
	api.calls = nil
	args[len(args)-1] = "--yes"
	if exit := Run(context.Background(), args, dependencies); exit != errx.OK {
		t.Fatalf("apply exit=%d output=%s", exit, stdout.String())
	}
	set, err := dependencies.Policies.Get(context.Background(), fresh)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reads) != 1 || len(set.Writes) != 0 {
		t.Fatalf("applied rebind=%+v", set)
	}
	if got, want := api.calls, []string{"conversations.info"}; !slices.Equal(got, want) {
		t.Fatalf("calls=%v want=%v", got, want)
	}
}

func TestPolicyResetRejectsWritePolicyAndCurrentBinding(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	if exit := Run(context.Background(), []string{"auth", "allow-writes", "set", "--profile", "work", "--conversation-id", "C1", "--reset-stale-policy", "--dry-run"}, dependencies); exit != errx.Usage {
		t.Fatalf("write reset exit=%d output=%s", exit, stdout.String())
	}
	if len(api.calls) != 0 {
		t.Fatalf("write reset reached Slack: %v", api.calls)
	}

	stdout.Reset()
	if _, err := dependencies.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1"}}); err != nil {
		t.Fatal(err)
	}
	if exit := Run(context.Background(), []string{"auth", "allow-reads", "set", "--profile", "work", "--conversation-id", "C1", "--reset-stale-policy", "--dry-run"}, dependencies); exit != errx.Usage {
		t.Fatalf("current reset exit=%d output=%s", exit, stdout.String())
	}
	if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "STALE_POLICY_REBIND_NOT_REQUIRED" {
		t.Fatalf("code=%v", code)
	}
}

func TestStalePolicyRebindNoopHasStableConflictEnvelope(t *testing.T) {
	t.Parallel()
	dependencies, _, _, stdout := newTestDependencies(t)
	if exit := dependencies.Output.Failure(translate(policy.ErrRebindNotNeeded)); exit != errx.Conflict {
		t.Fatalf("exit=%d output=%s", exit, stdout.String())
	}
	if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "STALE_POLICY_REBIND_NOT_REQUIRED" {
		t.Fatalf("code=%v", code)
	}
}

func TestConfirmedPolicySetRejectsEnterpriseIdentityDrift(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	api.conversation = slack.Conversation{ID: "D1", User: "U2", IsIM: true}
	api.user = slack.User{ID: "U2", TeamID: "T1"}
	api.identity.EnterpriseID = "E1"
	if exit := Run(context.Background(), []string{"auth", "allow-reads", "set", "--profile", "work", "--conversation-id", "D1", "--yes"}, dependencies); exit != errx.Conflict {
		t.Fatalf("exit=%d output=%s", exit, stdout.String())
	}
	if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "CREDENTIAL_IDENTITY_CHANGED" {
		t.Fatalf("code=%v", code)
	}
	set, err := dependencies.Policies.Get(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Reads) != 0 || len(set.Writes) != 0 {
		t.Fatalf("policy committed after identity drift: %+v", set)
	}
	if fmt.Sprint(api.calls) != fmt.Sprint([]string{"conversations.info", "users.info", "auth.test"}) {
		t.Fatalf("calls=%v", api.calls)
	}
}

func TestMissingOrgStateRechecksEnterpriseIdentityBeforeContent(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead})
	api.conversation = slack.Conversation{ID: "D1", User: "U2", IsIM: true}
	api.user = slack.User{ID: "U2", TeamID: "T1"}
	if exit := Run(context.Background(), []string{"auth", "allow-reads", "set", "--profile", "work", "--conversation-id", "D1", "--yes"}, dependencies); exit != errx.OK {
		t.Fatalf("initial policy exit=%d output=%s", exit, stdout.String())
	}
	api.identity.EnterpriseID = "E1"
	for _, args := range [][]string{
		{"messages", "history", "--profile", "work", "--conversation-id", "D1", "--limit", "25"},
		{"messages", "thread", "--profile", "work", "--conversation-id", "D1", "--thread-ts", "1.0", "--limit", "25"},
	} {
		api.calls = nil
		stdout.Reset()
		if exit := Run(context.Background(), args, dependencies); exit != errx.Conflict {
			t.Fatalf("args=%v exit=%d output=%s", args, exit, stdout.String())
		}
		if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "CREDENTIAL_IDENTITY_CHANGED" {
			t.Fatalf("args=%v code=%v", args, code)
		}
		if fmt.Sprint(api.calls) != fmt.Sprint([]string{"conversations.info", "users.info", "auth.test"}) {
			t.Fatalf("args=%v calls=%v", args, api.calls)
		}
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

func TestConfirmedSendRejectsEnterpriseIdentityDriftBeforePreflight(t *testing.T) {
	t.Parallel()
	dependencies, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, dependencies, store, []profile.Capability{profile.CapabilityRead, profile.CapabilityMessageWrite})
	seedWritePolicy(t, dependencies, p)
	dependencies.Input = bytes.NewBufferString("hello")
	if exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--dry-run"}, dependencies); exit != errx.OK {
		t.Fatalf("dry-run exit=%d output=%s", exit, stdout.String())
	}
	receipt := decodeEnvelope(t, stdout)["data"].(map[string]any)["intent_sha256"].(string)
	api.identity.EnterpriseID = "E1"
	api.calls = nil
	stdout.Reset()
	dependencies.Input = bytes.NewBufferString("hello")
	if exit := Run(context.Background(), []string{"messages", "send", "--profile", "work", "--conversation-id", "C1", "--text-stdin", "--confirm-intent", receipt, "--yes"}, dependencies); exit != errx.Conflict {
		t.Fatalf("exit=%d output=%s", exit, stdout.String())
	}
	if code := decodeEnvelope(t, stdout)["error"].(map[string]any)["code"]; code != "CREDENTIAL_IDENTITY_CHANGED" {
		t.Fatalf("code=%v", code)
	}
	if fmt.Sprint(api.calls) != fmt.Sprint([]string{"auth.test"}) {
		t.Fatalf("identity drift reached target or write preflight: %v", api.calls)
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
