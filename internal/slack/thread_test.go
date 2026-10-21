package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/errx"
)

func threadFixture(count int) []Message {
	messages := make([]Message, count)
	for index := range messages {
		messages[index] = Message{TS: fmt.Sprintf("%d.000001", index+1), ThreadTS: "1.000001"}
	}
	return messages
}

func TestThreadNativePagesAndOverlap(t *testing.T) {
	t.Parallel()
	// Refetch the oversized first page, then advance Slack's cursor. Repeated
	// parents and overlapping replies on subsequent native pages are filtered.
	pages := []struct {
		cursor string
		items  []Message
		next   string
		want   []string
	}{
		{"", threadFixture(3), "second", []string{"1.000001", "2.000001"}},
		{"", threadFixture(3), "second", []string{"3.000001"}},
		{"second", append(threadFixture(1), threadFixture(5)[2:]...), "third", []string{"4.000001", "5.000001"}},
		{"third", threadFixture(1), "fourth", nil},
		{"fourth", nil, "", nil},
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests >= len(pages) {
			t.Error("unexpected extra request")
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		page := pages[requests]
		requests++
		if r.URL.Query().Get("cursor") != page.cursor || r.URL.Query().Get("limit") != "2" {
			t.Errorf("query=%v want cursor=%q limit=2", r.URL.Query(), page.cursor)
		}
		if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": page.items, "response_metadata": map[string]string{"next_cursor": page.next}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	continuation := ""
	for index, fixture := range pages {
		page, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: 2, PageCursor: continuation})
		if err != nil {
			t.Fatalf("page %d: %v", index, err)
		}
		if len(page.Messages) != len(fixture.want) {
			t.Fatalf("page %d count=%d want=%d", index, len(page.Messages), len(fixture.want))
		}
		for position, want := range fixture.want {
			if page.Messages[position].TS != want {
				t.Fatalf("page %d message=%q want=%q", index, page.Messages[position].TS, want)
			}
		}
		continuation = page.NextCursor
		if (index < len(pages)-1) != (continuation != "") {
			t.Fatalf("page %d unexpected continuation=%q", index, continuation)
		}
	}
}

func TestThreadValidatesWholeRawPage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		items  []Message
		next   string
		cursor string
		code   string
	}{
		{name: "empty initial page", code: "SLACK_RESPONSE_INVALID"},
		{name: "missing parent", items: threadFixture(3)[1:], code: "SLACK_RESPONSE_INVALID"},
		{name: "wrong thread in overflow", items: []Message{{TS: "1.000001"}, {TS: "2.000001", ThreadTS: "other"}}, code: "SLACK_RESPONSE_INVALID"},
		{name: "malformed timestamp in overflow", items: []Message{{TS: "1.000001"}, {TS: "NaN", ThreadTS: "1.000001"}}, code: "SLACK_RESPONSE_INVALID"},
		{name: "unordered overflow", items: []Message{{TS: "1.000001"}, {TS: "3.0", ThreadTS: "1.000001"}, {TS: "2.0", ThreadTS: "1.000001"}}, code: "SLACK_RESPONSE_INVALID"},
		{name: "numeric duplicate", items: []Message{{TS: "1.000001"}, {TS: "2.0", ThreadTS: "1.000001"}, {TS: "2.00", ThreadTS: "1.000001"}}, code: "SLACK_RESPONSE_INVALID"},
		{name: "parent thread mismatch", items: []Message{{TS: "1.000001", ThreadTS: "9.0"}}, code: "SLACK_RESPONSE_INVALID"},
		{name: "raw hard cap", items: threadFixture(101), code: "SLACK_RESPONSE_COUNT_EXCEEDED"},
		{name: "repeated native cursor", items: threadFixture(2), cursor: "same", next: "same", code: "SLACK_RESPONSE_INVALID"},
		{name: "invalid native cursor", items: threadFixture(2), next: "bad\ncursor", code: "SLACK_RESPONSE_INVALID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": test.items, "response_metadata": map[string]string{"next_cursor": test.next}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := newTestClient(server.URL, server.Client())
			page, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: 1, Cursor: test.cursor})
			if err == nil || errx.As(err).Code != test.code || len(page.Messages) != 0 {
				t.Fatalf("page=%+v error=%v want=%s", page, err, test.code)
			}
		})
	}
}

func TestThreadLimitBoundsAndDeletedFrontier(t *testing.T) {
	t.Parallel()
	for _, limit := range []int{1, 100} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			items := threadFixture(100)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": items}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := newTestClient(server.URL, server.Client())
			page, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: limit})
			if err != nil || len(page.Messages) != limit {
				t.Fatalf("page=%+v error=%v", page, err)
			}
			if limit == 100 {
				if page.NextCursor != "" {
					t.Fatal("unexpected continuation")
				}
				return
			}
			// Removing the emitted anchor must not turn a positional offset into a skip.
			items = items[1:]
			second, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: limit, PageCursor: page.NextCursor})
			if err != nil || len(second.Messages) != 1 || second.Messages[0].TS != "2.000001" {
				t.Fatalf("after deletion: page=%+v error=%v", second, err)
			}
		})
	}
}

func TestThreadCursorDomains(t *testing.T) {
	t.Parallel()
	legacy := `{"v":1,"upstream":"legacy","after":"2.0"}`
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("cursor") != legacy {
			t.Errorf("legacy cursor changed: %q", r.URL.Query().Get("cursor"))
		}
		_, _ = w.Write([]byte(`{"ok":true,"messages":[{"ts":"2.000001","thread_ts":"1.000001"}]}`))
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	if _, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: 50, Cursor: legacy}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"{}", legacy + " ", `{"v":2,"upstream":"x","after":""}`, `{"v":1,"upstream":"","after":""}`, strings.Repeat("x", 16385)} {
		_, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: 50, PageCursor: invalid})
		if err == nil || errx.As(err).Code != "INVALID_CURSOR" {
			t.Fatalf("accepted invalid continuation: %v", err)
		}
	}
	if requests != 1 {
		t.Fatalf("invalid cursor dispatched %d requests", requests-1)
	}
}

func TestTimeFilteredThreadKeepsStrictBound(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": threadFixture(2)}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	for _, options := range []ThreadOptions{{Oldest: "1.0"}, {Latest: "3.0"}, {Inclusive: true}} {
		options.Limit, options.ConversationID, options.ThreadTS = 1, "C1", "1.000001"
		_, err := client.Replies(context.Background(), Token("sentinel"), options)
		if err == nil || errx.As(err).Code != "SLACK_RESPONSE_COUNT_EXCEEDED" {
			t.Fatalf("filtered proof bound changed: %v", err)
		}
	}
}
