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

func fixtureBotToken() string { return "xox" + "b-super-secret" }

func TestAuthTestUsesFixedRouteAndHeader(t *testing.T) {
	t.Parallel()
	sentinel := fixtureBotToken()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth.test" || r.Method != http.MethodPost {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer "+sentinel {
			t.Fatal("missing bearer token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"team":"Example","team_id":"T1","url":"https://example.slack.com/","user":"bot","user_id":"U1","bot_id":"B1"}`))
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
	sentinel := fixtureBotToken()
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

func TestReadFailureStagesAreFixedAndRedacted(t *testing.T) {
	t.Parallel()
	sentinel := fixtureBotToken()
	tests := []struct {
		name           string
		stage          errx.Stage
		handler        http.HandlerFunc
		transportError bool
		cancelBefore   bool
	}{
		{name: "pre-dispatch", stage: errx.StagePreDispatch, cancelBefore: true},
		{name: "transport", stage: errx.StageTransport, transportError: true},
		{name: "server", stage: errx.StageHTTPServer, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{name: "rate limit response", stage: errx.StageRateLimitResponse, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "invalid")
			w.WriteHeader(http.StatusTooManyRequests)
		}},
		{name: "response body", stage: errx.StageResponseBody, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write([]byte("not-gzip"))
		}},
		{name: "response JSON", stage: errx.StageResponseJSON, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{"))
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var client *Client
			var closeServer func()
			if test.transportError {
				httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return nil, errors.New("transport failed with " + sentinel)
				})}
				client = newTestClient("https://slack.invalid", httpClient)
			} else {
				handler := test.handler
				if handler == nil {
					handler = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
				}
				server := httptest.NewServer(handler)
				closeServer = server.Close
				client = newTestClient(server.URL, server.Client())
			}
			if closeServer != nil {
				defer closeServer()
			}
			ctx := context.Background()
			if test.cancelBefore {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			_, err := client.AuthTest(ctx, Token(sentinel))
			if err == nil {
				t.Fatal("expected error")
			}
			typed := errx.As(err)
			if typed.Exit != errx.Retryable || typed.Code != "SLACK_READ_FAILED" || typed.Stage != test.stage {
				t.Fatalf("error=%+v", typed)
			}
			if strings.Contains(typed.Error(), sentinel) || strings.Contains(typed.Message, sentinel) || strings.Contains(string(typed.Stage), sentinel) {
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

func TestUserInfoProjectsStrangerInternally(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users.info" || r.Method != http.MethodGet {
			t.Fatalf("request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"ok":true,"user":{"id":"U2","team_id":"T1","is_stranger":true}}`))
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	user, err := client.UserInfo(context.Background(), Token("sentinel"), "U2")
	if err != nil {
		t.Fatal(err)
	}
	if !user.IsStranger {
		t.Fatal("is_stranger was not projected")
	}
	payload, err := json.Marshal(user)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "is_stranger") {
		t.Fatalf("internal stranger classification leaked: %s", payload)
	}
}

func TestOnlyOneJSONObjectAccepted(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(authTestEnvelope{OK: true, TeamID: "T1"})
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

func TestPostDispatchFailuresRemainOneShotConflicts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		stage          errx.Stage
		handler        http.HandlerFunc
		transportError bool
	}{
		{name: "transport", stage: errx.StageTransport, transportError: true},
		{name: "server", stage: errx.StageHTTPServer, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}},
		{name: "malformed JSON", stage: errx.StageResponseJSON, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{"))
		}},
		{name: "bad gzip", stage: errx.StageResponseBody, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write([]byte("not-gzip"))
		}},
		{name: "invalid retry after", stage: errx.StageRateLimitResponse, handler: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Retry-After", "invalid")
			w.WriteHeader(http.StatusTooManyRequests)
		}},
		{name: "unknown API error", stage: errx.StageAPIError, handler: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"ok":false,"error":"unexpected_failure"}`))
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			var client *Client
			var closeServer func()
			if test.transportError {
				httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					calls++
					return nil, errors.New("transport failed")
				})}
				client = newTestClient("https://slack.invalid", httpClient)
			} else {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					test.handler(w, r)
				}))
				closeServer = server.Close
				client = newTestClient(server.URL, server.Client())
			}
			if closeServer != nil {
				defer closeServer()
			}
			_, err := client.PostMessage(context.Background(), Token("sentinel"), PostOptions{ConversationID: "C1", Text: "hello"})
			if err == nil {
				t.Fatal("expected error")
			}
			typed := errx.As(err)
			if typed.Exit != errx.Conflict || typed.Code != "WRITE_OUTCOME_UNKNOWN" || typed.Stage != test.stage {
				t.Fatalf("error=%+v", typed)
			}
			if calls != 1 {
				t.Fatalf("write dispatched %d times", calls)
			}
		})
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}
