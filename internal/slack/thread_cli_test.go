package slack_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/auth"
	"github.com/abigotado/slack-agent-cli/internal/cli"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

type readCredentialStore struct{ value auth.Credential }

func (s readCredentialStore) Load(context.Context, string) (auth.Credential, error) {
	return s.value, nil
}
func (readCredentialStore) Save(context.Context, string, auth.Credential) error {
	return errors.New("unexpected credential write")
}
func (readCredentialStore) Delete(context.Context, string) error {
	return errors.New("unexpected credential deletion")
}

func TestThreadWithoutLimitPaginatesOversizedSlackPage(t *testing.T) {
	t.Parallel()
	parent := "1.000001"
	messages := make([]slack.Message, 69)
	for i := range messages {
		messages[i] = slack.Message{Type: "message", TS: fmt.Sprintf("%d.000001", i+1), ThreadTS: parent, Text: "fixture"}
	}
	messages[0].ReplyCount = 68
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/conversations.info":
			_, _ = w.Write([]byte(`{"ok":true,"channel":{"id":"C1","is_channel":true,"is_shared":false,"is_ext_shared":false,"is_org_shared":false}}`))
		case "/api/conversations.replies":
			requests++
			if r.URL.Query().Get("limit") != "50" || r.URL.Query().Get("cursor") != "" {
				t.Errorf("unexpected Slack pagination query: %v", r.URL.Query())
			}
			// Slack's oversized page must be resumed rather than silently truncated.
			if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": messages}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected route: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := slack.NewTestClient(server.URL, server.Client())
	root := t.TempDir()
	profiles := profile.NewRegistry(filepath.Join(root, "config", "profiles.json"))
	policies := policy.NewRegistry(filepath.Join(root, "config", "policies.json"))
	p := profile.Profile{Name: "work", WorkspaceID: "T1", WorkspaceName: "Example", WorkspaceURL: "https://example.slack.com", UserID: "U1", TokenKind: profile.TokenUser, Capabilities: []profile.Capability{profile.CapabilityRead}, CredentialGeneration: "g1"}
	if err := profiles.Put(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, err := policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1"}}); err != nil {
		t.Fatal(err)
	}
	stdout := &bytes.Buffer{}
	d := cli.Dependencies{Profiles: profiles, Policies: policies, Credentials: readCredentialStore{auth.Credential{Version: 1, Token: "sentinel", ProfileIdentity: profile.Identity(p), Generation: "g1", Capabilities: p.Capabilities}}, Conversations: client, Messages: client, Output: &output.Writer{Out: stdout, Err: &bytes.Buffer{}}}
	cursor := ""
	seen := map[string]bool{}
	for page, want := range []int{50, 19} {
		args := []string{"messages", "thread", "--profile", "work", "--conversation-id", "C1", "--thread-ts", parent}
		if cursor != "" {
			args = append(args, "--cursor", cursor)
		}
		stdout.Reset()
		if exit := cli.Run(context.Background(), args, d); exit != errx.OK {
			t.Fatalf("page %d exit=%d output=%s", page+1, exit, stdout)
		}
		var result struct {
			OK   bool            `json:"ok"`
			Data []slack.Message `json:"data"`
			Meta struct {
				NextCursor   string `json:"next_cursor"`
				ContentTrust string `json:"content_trust"`
				Count        int    `json:"count"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.OK || len(result.Data) != want || result.Meta.Count != want || result.Meta.ContentTrust != "untrusted" {
			t.Fatalf("unexpected page: %s", stdout)
		}
		for _, message := range result.Data {
			if seen[message.TS] {
				t.Fatalf("duplicate timestamp: %s", message.TS)
			}
			seen[message.TS] = true
		}
		cursor = result.Meta.NextCursor
		if (page == 0) != (cursor != "") {
			t.Fatalf("unexpected continuation on page %d", page+1)
		}
	}
	if len(seen) != 69 || !seen[parent] || requests != 2 {
		t.Fatalf("lost messages or excess requests: unique=%d requests=%d", len(seen), requests)
	}
}
