package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

func threadFixture(count int) []Message {
	messages := make([]Message, count)
	for index := range messages {
		messages[index] = Message{TS: fmt.Sprintf("%d.000001", index+1), ThreadTS: "1.000001"}
	}
	return messages
}

func TestThreadForwardNativePagesAndEmptyAdvance(t *testing.T) {
	t.Parallel()
	// Refetch the oversized first page, then advance Slack's cursor. Repeated
	// parents are filtered, while page-local overflow is consumed before advancing.
	pages := []struct {
		cursor string
		items  []Message
		next   string
		want   []string
	}{
		{"", threadFixture(3), "second", []string{"1.000001", "2.000001"}},
		{"", threadFixture(3), "second", []string{"3.000001"}},
		{"second", append(threadFixture(1), threadFixture(5)[3:]...), "third", []string{"4.000001", "5.000001"}},
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
		if index < len(pages)-1 {
			var state threadContinuation
			if err := json.Unmarshal([]byte(continuation), &state); err != nil {
				t.Fatal(err)
			}
			if index == 0 && (state.PreviousMin != "" || state.After != "2.000001") {
				t.Fatalf("advanced before consuming overflow: %+v", state)
			}
			if index == 1 && (state.After != "" || state.PreviousMin != "2.000001" || state.PreviousMax != "3.000001") {
				t.Fatalf("did not reset page frontier: %+v", state)
			}
			if index == 3 && (state.Direction != 1 || state.PreviousMin != "4.000001" || state.PreviousMax != "5.000001") {
				t.Fatalf("empty page lost completed range: %+v", state)
			}
		}
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
		{name: "numeric parent alias on continuation", items: []Message{{TS: "1.0000010", ThreadTS: "1.000001"}}, cursor: "native", code: "SLACK_RESPONSE_INVALID"},
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
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if want := strconv.Itoa(min(limit, 99)); r.URL.Query().Get("limit") != want {
					t.Errorf("upstream limit=%q want=%q", r.URL.Query().Get("limit"), want)
				}
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
			items = items[1:]
			third, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: limit, PageCursor: second.NextCursor})
			if err != nil || len(third.Messages) != 1 || third.Messages[0].TS != "3.000001" {
				t.Fatalf("deleted reply anchor: page=%+v error=%v", third, err)
			}
			var state threadContinuation
			if err := json.Unmarshal([]byte(third.NextCursor), &state); err != nil {
				t.Fatal(err)
			}
			if state.PageMin != "2.000001" {
				t.Fatalf("lost observed page range after deletion: %+v", state)
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
	for _, invalid := range []string{"{}", legacy, legacy + " ", `{"v":2,"upstream":"x","after":""}`, `{"v":1,"upstream":"","after":""}`, strings.Repeat("x", 16385)} {
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

func TestThreadRejectsReplyTimestampAsInput(t *testing.T) {
	t.Parallel()
	for _, items := range [][]Message{threadFixture(3), threadFixture(1)} {
		t.Run(fmt.Sprint(len(items)), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": items}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := newTestClient(server.URL, server.Client())
			_, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "2.000001", Limit: 1})
			if err == nil || errx.As(err).Code != "THREAD_PARENT_REQUIRED" || errx.ProcessExit(err) != errx.Usage {
				t.Fatalf("wrong parent error: %v", err)
			}
		})
	}
}

func TestThreadContinuationGuardsBeforeDispatch(t *testing.T) {
	t.Parallel()
	client := newTestClient("https://slack.invalid", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Error("invalid continuation dispatched a request")
		return nil, fmt.Errorf("unexpected dispatch")
	})})
	continuation := `{"v":2,"upstream":"native","after":"2.000001","previous_min":"","previous_max":"","page_min":"2.000001","page_max":"3.000001","direction":0}`
	for _, options := range []ThreadOptions{
		{Cursor: "native", PageCursor: continuation},
		{Oldest: "1.0", PageCursor: continuation},
		{Latest: "3.0", PageCursor: continuation},
		{Inclusive: true, PageCursor: continuation},
	} {
		options.Limit, options.ConversationID, options.ThreadTS = 1, "C1", "1.000001"
		if _, err := client.Replies(context.Background(), Token("sentinel"), options); err == nil || errx.As(err).Code != "INVALID_CURSOR" {
			t.Fatalf("invalid cursor guard: %v", err)
		}
	}
}

func TestThreadCanonicalContinuationGuardsBeforeDispatch(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*threadContinuation)
	}{
		{"valid continuation", nil},
		{"obsolete version", func(s *threadContinuation) { s.Version = 1 }},
		{"direction above bound", func(s *threadContinuation) { s.Direction = 2 }},
		{"direction below bound", func(s *threadContinuation) { s.Direction = -2 }},
		{"direction without previous range", func(s *threadContinuation) { s.Direction = 1 }},
		{"inverted page range", func(s *threadContinuation) { s.PageMin, s.PageMax = "4.0", "2.0" }},
		{"inverted previous range", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax = "7.0", "6.0" }},
		{"after without page range", func(s *threadContinuation) { s.PageMin, s.PageMax = "", "" }},
		{"page range without after", func(s *threadContinuation) { s.After = "" }},
		{"after above page range", func(s *threadContinuation) { s.After = "5.0" }},
		{"after below page range", func(s *threadContinuation) { s.After = "1.5" }},
		{"previous minimum is parent", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax = "1.000001", "1.5" }},
		{"previous minimum before parent", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax = "1.0", "1.5" }},
		{"page minimum is parent", func(s *threadContinuation) { s.PageMin = "1.000001" }},
		{"page minimum before parent", func(s *threadContinuation) { s.PageMin = "1.0" }},
		{"overlapping ranges", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax = "3.0", "5.0" }},
		{"ascending direction disagrees", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax, s.Direction = "6.0", "7.0", 1 }},
		{"descending direction disagrees", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax, s.Direction = "1.5", "1.9", -1 }},
		{"direction missing for disjoint ranges", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax = "6.0", "7.0" }},
		{"previous minimum missing", func(s *threadContinuation) { s.PreviousMax = "7.0" }},
		{"previous maximum missing", func(s *threadContinuation) { s.PreviousMin = "6.0" }},
		{"page minimum missing", func(s *threadContinuation) { s.PageMin = "" }},
		{"page maximum missing", func(s *threadContinuation) { s.PageMax = "" }},
		{"malformed previous minimum", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax = "NaN", "7.0" }},
		{"malformed previous maximum", func(s *threadContinuation) { s.PreviousMin, s.PreviousMax = "6.0", "NaN" }},
		{"malformed page minimum", func(s *threadContinuation) { s.PageMin = "NaN" }},
		{"malformed page maximum", func(s *threadContinuation) { s.PageMax = "NaN" }},
		{"malformed after", func(s *threadContinuation) { s.After = "NaN" }},
		{"invalid native cursor", func(s *threadContinuation) { s.Upstream = "bad\ncursor" }},
		{"empty continuation", func(s *threadContinuation) { s.Upstream, s.After, s.PageMin, s.PageMax = "", "", "", "" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := threadContinuation{Version: 2, Upstream: "native", After: "3.0", PageMin: "2.0", PageMax: "4.0"}
			if test.mutate != nil {
				test.mutate(&state)
			}
			encoded, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			requests := 0
			client := newTestClient("https://slack.invalid", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				requests++
				return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true,"messages":[{"ts":"4.0","thread_ts":"1.000001"}]}`))}, nil
			})})
			page, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: 50, PageCursor: string(encoded)})
			if test.mutate == nil {
				if err != nil || requests != 1 || len(page.Messages) != 1 || page.Messages[0].TS != "4.0" || page.NextCursor != "" {
					t.Fatalf("valid base continuation rejected: requests=%d page=%+v error=%v", requests, page, err)
				}
				return
			}
			if err == nil || errx.As(err).Code != "INVALID_CURSOR" || errx.ProcessExit(err) != errx.Usage || requests != 0 || len(page.Messages) != 0 || page.NextCursor != "" {
				t.Fatalf("invalid canonical cursor: requests=%d page=%+v error=%v", requests, page, err)
			}
		})
	}
}

func TestThreadRejectsOversizedEncodedContinuation(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": threadFixture(1), "response_metadata": map[string]string{"next_cursor": strings.Repeat("x", contract.MaxCursorBytes)}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	client := newTestClient(server.URL, server.Client())
	page, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: 50})
	if err == nil || errx.As(err).Code != "SLACK_RESPONSE_INVALID" || page.NextCursor != "" || len(page.Messages) != 0 {
		t.Fatalf("unsafe oversized continuation: page=%+v error=%v", page, err)
	}
}

func TestThreadNativeRangeOrder(t *testing.T) {
	t.Parallel()
	reply := func(ts string) Message { return Message{TS: ts, ThreadTS: "1.000001"} }
	parent := Message{TS: "1.000001"}
	for _, test := range []struct {
		name       string
		pages      [][]Message
		rejectLast bool
	}{
		{"descending", [][]Message{{parent, reply("8.0"), reply("9.0")}, {reply("6.0"), reply("7.0")}, {reply("4.0"), reply("5.0")}}, false},
		{"ascending overlap", [][]Message{{parent, reply("2.0"), reply("3.0")}, {reply("3.0"), reply("4.0")}}, true},
		{"descending overlap", [][]Message{{parent, reply("4.0"), reply("5.0")}, {reply("3.0"), reply("4.0")}}, true},
		{"unseen gap inside previous range", [][]Message{{parent, reply("4.0"), reply("8.0")}, {reply("5.0"), reply("7.0")}}, true},
		{"direction reversal", [][]Message{{parent, reply("8.0"), reply("9.0")}, {reply("6.0"), reply("7.0")}, {reply("10.0"), reply("11.0")}}, true},
		{"direction after empty page", [][]Message{{parent, reply("8.0"), reply("9.0")}, {reply("6.0"), reply("7.0")}, {parent}, {reply("10.0"), reply("11.0")}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				index := requests
				requests++
				if index >= len(test.pages) {
					t.Error("unexpected extra request")
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				wantCursor := ""
				if index > 0 {
					wantCursor = fmt.Sprintf("page%d", index)
				}
				if r.URL.Query().Get("cursor") != wantCursor {
					t.Errorf("cursor=%q want=%q", r.URL.Query().Get("cursor"), wantCursor)
				}
				next := ""
				if index < len(test.pages)-1 {
					next = fmt.Sprintf("page%d", index+1)
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"ok": true, "messages": test.pages[index], "response_metadata": map[string]string{"next_cursor": next}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			client := newTestClient(server.URL, server.Client())
			continuation := ""
			seen := map[string]bool{}
			for index := range test.pages {
				page, err := client.Replies(context.Background(), Token("sentinel"), ThreadOptions{ConversationID: "C1", ThreadTS: "1.000001", Limit: 3, PageCursor: continuation})
				if test.rejectLast && index == len(test.pages)-1 {
					if err == nil || errx.As(err).Code != "SLACK_RESPONSE_INVALID" || len(page.Messages) != 0 || page.NextCursor != "" {
						t.Fatalf("accepted overlapping/reversing range: page=%+v error=%v", page, err)
					}
					if errx.ProcessExit(err) != errx.Internal || errx.As(err).Hint != "restart this thread read once from the first page without --cursor; if this happens again, stop and report it" {
						t.Fatalf("missing safe restart guidance: %+v", errx.As(err))
					}
					break
				}
				if err != nil {
					t.Fatalf("page%d: %v", index, err)
				}
				for _, message := range page.Messages {
					if seen[message.TS] {
						t.Fatalf("duplicate timestamp %s", message.TS)
					}
					seen[message.TS] = true
				}
				continuation = page.NextCursor
				if (index < len(test.pages)-1) != (continuation != "") {
					t.Fatalf("unexpected next cursor on page%d", index)
				}
			}
		})
	}
}
