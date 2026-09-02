package slack

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/errx"
)

func TestAuthTestUsesFixedRouteAndHeader(t *testing.T) {
	t.Parallel()
	sentinel := "xoxb-super-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth.test" || r.Method != http.MethodPost {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+sentinel {
			t.Fatal("missing bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"team":"Example","team_id":"T1","url":"https://example.slack.com/","user_id":"U1","bot_id":"B1"}`))
	}))
	defer server.Close()
	client := newTestClient(server.URL, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
	identity, err := client.AuthTest(context.Background(), Token(sentinel))
	if err != nil {
		t.Fatal(err)
	}
	if identity.WorkspaceID != "T1" || identity.UserID != "U1" || identity.BotID != "B1" {
		t.Fatalf("bad identity: %+v", identity)
	}
}

func TestSlackFailuresAreBoundedAndRedacted(t *testing.T) {
	t.Parallel()
	sentinel := "xoxb-super-secret"
	tests := []struct {
		name    string
		handler http.HandlerFunc
		exit    errx.Exit
	}{
		{"api auth", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
		}, errx.Auth},
		{"rate limit", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "12")
			w.WriteHeader(http.StatusTooManyRequests)
		}, errx.Retryable},
		{"redirect", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "https://evil.example")
			w.WriteHeader(http.StatusFound)
		}, errx.Internal},
		{"trailing JSON", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"ok":true,"team_id":"T1"}{}`)) }, errx.Retryable},
		{"oversize", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(bytes.Repeat([]byte{'x'}, (8<<20)+1)) }, errx.Retryable},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			client := newTestClient(server.URL, &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
			_, err := client.AuthTest(context.Background(), Token(sentinel))
			if err == nil {
				t.Fatal("expected error")
			}
			typed := errx.As(err)
			if typed.Exit != test.exit {
				t.Fatalf("exit %d want %d", typed.Exit, test.exit)
			}
			if strings.Contains(err.Error(), sentinel) || strings.Contains(typed.Message, sentinel) {
				t.Fatal("secret leaked")
			}
		})
	}
}

func TestCompressedAndDecompressedBounds(t *testing.T) {
	t.Parallel()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(bytes.Repeat([]byte{'x'}, (8<<20)+1))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(compressed.Bytes())
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	_, err := client.AuthTest(context.Background(), Token("sentinel"))
	if err == nil {
		t.Fatal("decompression bomb accepted")
	}
	if errx.As(err).Exit != errx.Retryable {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConversationClassificationFailsClosed(t *testing.T) {
	t.Parallel()
	yes, no := true, false
	tests := []struct {
		name         string
		conversation Conversation
		known        bool
	}{{"complete", Conversation{IsShared: &no, IsExtShared: &no, IsOrgShared: &yes}, true}, {"missing", Conversation{IsShared: &no, IsExtShared: &no}, false}}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			_, known := test.conversation.ClassifyShared()
			if known != test.known {
				t.Fatalf("known=%v", known)
			}
		})
	}
}

func TestOnlyOneJSONObjectAccepted(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(apiEnvelope{OK: true, TeamID: "T1"})
	payload = append(payload, '\n')
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	if _, err := client.AuthTest(context.Background(), Token("sentinel")); err != nil {
		t.Fatal(err)
	}
	_ = errors.Is
}

func TestCollectionResponseCannotExceedRequestedLimit(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"1.0"},{"ts":"2.0"}]}`))
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	_, err := client.History(context.Background(), Token("sentinel"), HistoryOptions{ConversationID: "C1", Limit: 1})
	if err == nil || errx.As(err).Code != "SLACK_RESPONSE_COUNT_EXCEEDED" {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestPostMessageIsOneShotAndDisablesUnfurls(t *testing.T) {
	t.Parallel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/chat.postMessage" || r.Method != http.MethodPost {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("channel") != "C1" || r.Form.Get("text") != "hello" || r.Form.Get("unfurl_links") != "false" || r.Form.Get("unfurl_media") != "false" {
			t.Fatalf("form %v", r.Form)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	_, err := client.PostMessage(context.Background(), Token("sentinel"), PostOptions{ConversationID: "C1", Text: "hello"})
	if err == nil || errx.As(err).Code != "WRITE_OUTCOME_UNKNOWN" {
		t.Fatalf("unexpected error %v", err)
	}
	if calls != 1 {
		t.Fatalf("write replayed %d times", calls)
	}
}

func TestPostMessageNonCommittingErrorAllowlist(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope"}`))
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	_, err := client.PostMessage(context.Background(), Token("sentinel"), PostOptions{ConversationID: "C1", Text: "hello"})
	if err == nil || errx.As(err).Exit != errx.PermissionDenied {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestPostMessageEnforcesV1InputAtTransportBoundary(t *testing.T) {
	t.Parallel()
	client := newTestClient("http://127.0.0.1", &http.Client{})
	tests := []PostOptions{
		{ConversationID: "bad-id", Text: "hello"},
		{ConversationID: "C1", ThreadTS: "1.", Text: "hello"},
		{ConversationID: "C1", Text: ""},
		{ConversationID: "C1", Text: "hello\x00world"},
		{ConversationID: "C1", Text: strings.Repeat("a", (16<<10)+1)},
		{ConversationID: "C1", Text: strings.Repeat("é", 4001)},
		{ConversationID: "C1", Text: string([]byte{0xff})},
	}
	for _, options := range tests {
		if _, err := client.PostMessage(context.Background(), Token("sentinel"), options); err == nil || errx.As(err).Exit != errx.Usage {
			t.Fatalf("options=%+v error=%v", options, err)
		}
	}
}
