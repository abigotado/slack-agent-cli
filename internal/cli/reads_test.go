package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

func TestPaginatedCommandLimits(t *testing.T) {
	t.Parallel()
	commands := []struct {
		name string
		args []string
	}{
		{"list", []string{"conversations", "list", "--types", "public_channel"}},
		{"history", []string{"messages", "history", "--conversation-id", "C1"}},
		{"thread", []string{"messages", "thread", "--conversation-id", "C1", "--thread-ts", "1.0"}},
	}
	for _, command := range commands {
		for _, test := range []struct {
			name  string
			flags []string
			limit int
			exit  errx.Exit
		}{
			{"default", nil, 50, errx.OK},
			{"explicit", []string{"--limit", "25"}, 25, errx.OK},
			{"minimum", []string{"--limit", "1"}, 1, errx.OK},
			{"maximum", []string{"--limit", "100"}, 100, errx.OK},
			{"zero", []string{"--limit", "0"}, 0, errx.Usage},
			{"negative", []string{"--limit", "-1"}, -1, errx.Usage},
			{"too large", []string{"--limit", "101"}, 101, errx.Usage},
		} {
			t.Run(command.name+"/"+test.name, func(t *testing.T) {
				t.Parallel()
				d, store, api, stdout := newTestDependencies(t)
				p := seedSession(t, d, store, []profile.Capability{profile.CapabilityRead})
				no := false
				api.conversation = slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
				api.messages.Messages = []slack.Message{}
				if _, err := d.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1"}}); err != nil {
					t.Fatal(err)
				}
				args := append(slices.Clone(command.args), "--profile", "work")
				args = append(args, test.flags...)
				if exit := Run(context.Background(), args, d); exit != test.exit {
					t.Fatalf("exit=%d want=%d output=%s", exit, test.exit, stdout)
				}
				envelope := decodeEnvelope(t, stdout)
				if test.exit != errx.OK {
					if envelope["ok"] != false || envelope["error"].(map[string]any)["code"] != "INVALID_LIMIT" {
						t.Fatalf("output=%s", stdout)
					}
					if len(api.calls) != 0 || store.loads != 0 {
						t.Fatalf("invalid limit reached credentials or network: %v", api.calls)
					}
					return
				}
				if envelope["ok"] != true {
					t.Fatalf("output=%s", stdout)
				}
				var got int
				switch command.name {
				case "list":
					got = api.listOptions[0].Limit
				case "history":
					got = api.historyOptions[0].Limit
				case "thread":
					got = api.replyOptions[0].Limit
				}
				if got != test.limit {
					t.Fatalf("requested limit=%d want=%d", got, test.limit)
				}
			})
		}
	}
}

func TestThreadWithoutLimitReturnsParentAndReplies(t *testing.T) {
	t.Parallel()
	d, store, api, stdout := newTestDependencies(t)
	p := seedSession(t, d, store, []profile.Capability{profile.CapabilityRead})
	no := false
	api.conversation = slack.Conversation{ID: "C1", IsChannel: true, IsShared: &no, IsExtShared: &no, IsOrgShared: &no}
	api.messages.Messages = []slack.Message{
		{TS: "1.0", ThreadTS: "1.0", Text: "parent"},
		{TS: "2.0", ThreadTS: "1.0", Text: "first reply"},
		{TS: "3.0", ThreadTS: "1.0", Text: "second reply"},
	}
	if _, err := d.Policies.Replace(context.Background(), p, policy.Read, []policy.Target{{ConversationID: "C1"}}); err != nil {
		t.Fatal(err)
	}
	args := []string{"messages", "thread", "--profile", "work", "--conversation-id", "C1", "--thread-ts", "1.0"}
	if exit := Run(context.Background(), args, d); exit != errx.OK {
		t.Fatalf("exit=%d output=%s", exit, stdout)
	}
	if len(api.replyOptions) != 1 || api.replyOptions[0].Limit != 50 || api.replyOptions[0].ThreadTS != "1.0" {
		t.Fatalf("requests=%+v", api.replyOptions)
	}
	envelope := decodeEnvelope(t, stdout)
	data := envelope["data"].([]any)
	if len(data) != 3 {
		t.Fatalf("messages=%v", data)
	}
	for i, want := range api.messages.Messages {
		got := data[i].(map[string]any)
		if got["ts"] != want.TS || got["text"] != want.Text || got["thread_ts"] != "1.0" {
			t.Fatalf("message=%v want=%+v", got, want)
		}
	}
	meta := envelope["meta"].(map[string]any)
	if meta["count"] != float64(3) || meta["content_trust"] != "untrusted" {
		t.Fatalf("meta=%v", meta)
	}
}

func TestHelpDocumentsLimitsAndProcessStatus(t *testing.T) {
	t.Parallel()
	for _, command := range [][]string{nil, {"conversations", "list"}, {"messages", "history"}, {"messages", "thread"}} {
		t.Run(fmt.Sprint(command), func(t *testing.T) {
			d, _, _, stdout := newTestDependencies(t)
			if exit := Run(context.Background(), append(slices.Clone(command), "--help"), d); exit != errx.OK {
				t.Fatalf("exit=%d output=%s", exit, stdout)
			}
			for _, required := range []string{"every ok:false response exits nonzero", "1: API, authentication, or internal failure", "2: usage or input error", "pipefail"} {
				if !strings.Contains(stdout.String(), required) {
					t.Fatalf("help missing %q: %s", required, stdout)
				}
			}
			if len(command) > 0 && !strings.Contains(stdout.String(), "page size 1-100 (default 50)") {
				t.Fatalf("help missing default limit: %s", stdout)
			}
		})
	}
}

func TestRunProcessStatusMatchesEnvelope(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		exit errx.Exit
	}{
		{"success", nil, errx.OK},
		{"API", errx.New(errx.Internal, "SLACK_API_ERROR", "unsupported API error", "inspect"), errx.Internal},
		{"auth", errx.New(errx.Auth, "SLACK_AUTH_REJECTED", "credential rejected", "login"), errx.Internal},
		{"retryable", errx.New(errx.Retryable, "SLACK_READ_RETRYABLE", "read failed", "back off"), errx.Internal},
		{"remote permission", errx.New(errx.PermissionDenied, "SLACK_PERMISSION_DENIED", "denied", "request scope"), errx.Internal},
		{"local credential", errx.New(errx.Auth, "CREDENTIAL_NOT_FOUND", "missing", "login"), errx.Internal},
		{"input", usageError("INVALID_PROFILE", "invalid profile", nil), errx.Usage},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, store, api, stdout := newTestDependencies(t)
			seedSession(t, d, store, []profile.Capability{profile.CapabilityRead})
			api.authErr = test.err
			if exit := Run(context.Background(), []string{"me", "--profile", "work"}, d); exit != test.exit {
				t.Fatalf("exit=%d want=%d output=%s", exit, test.exit, stdout)
			}
			envelope := decodeEnvelope(t, stdout)
			if envelope["ok"] != (test.exit == errx.OK) {
				t.Fatalf("exit and envelope disagree: %s", stdout)
			}
			if test.err != nil && envelope["error"].(map[string]any)["code"] != errx.As(test.err).Code {
				t.Fatalf("error changed: %s", stdout)
			}
		})
	}
}
