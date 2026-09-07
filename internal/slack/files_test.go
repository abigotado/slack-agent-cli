package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

func TestFileInfoProjectionAndExactIdentity(t *testing.T) {
	for _, id := range []string{"F1", "F2"} {
		t.Run(id, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/files.info" || r.URL.Query().Get("file") != "F1" || r.URL.Query().Get("limit") != "1" || r.Header.Get("Authorization") != "Bearer sentinel" {
					t.Error("incorrect typed file request")
				}
				_, _ = io.WriteString(w, `{"ok":true,"file":{"id":"`+id+`","size":3,"mode":"hosted","url_private":"https://files.slack.com/files-pri/T1-F1/a.mp4","preview":"secret preview"},"comments":[{"text":"hidden"}]}`)
			}))
			defer server.Close()
			file, err := newTestClient(server.URL, server.Client()).FileInfo(context.Background(), "sentinel", "F1")
			if id == "F2" {
				if err == nil {
					t.Fatal("accepted wrong file")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(file)
			if err != nil {
				t.Fatal(err)
			}
			if file.downloadURL == "" || strings.Contains(string(encoded), "https") || strings.Contains(string(encoded), "preview") || strings.Contains(string(encoded), "hidden") {
				t.Fatalf("projection leaked private fields: %s", encoded)
			}
		})
	}
}

// fileTransport keeps production URL validation intact while testing HTTP responses.
type fileTransport func(*http.Request) (*http.Response, error)

func (f fileTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDownloadURLBoundary(t *testing.T) {
	for _, endpoint := range []string{
		"http://files.slack.com/files-pri/T1-F1/a", "https://files.slack.com.evil.test/files-pri/T1-F1/a",
		"https://files.slack.com:443/files-pri/T1-F1/a", "https://user@files.slack.com/files-pri/T1-F1/a",
		"https://files.slack.com/files-pri/T2-F1/a", "https://files.slack.com/files-pri/T1-F2/a",
		"https://files.slack.com/files-pri/T1-F1/a?token=secret", "https://files.slack.com/files-pri/T1-F1/a#x",
		"https://files.slack.com/files-pri/T1-F1/../a", "https://files.slack.com/files-pri/T1-F1/%2e%2e/a",
		"https://files.slack.com/files-pri/T1-F1/%2fa/../../a", "https://files.slack.com/files-pri/T1-F1/a%5cb",
		"https://files.slack.com/files-pri/T1-F1/",
	} {
		t.Run(endpoint, func(t *testing.T) {
			calls := 0
			c := &Client{httpClient: &http.Client{Transport: fileTransport(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected") })}}
			_, err := c.DownloadFile(context.Background(), "sentinel", FileDetails{File: File{ID: "F1", Mode: "hosted"}, downloadURL: endpoint}, "T1", io.Discard)
			if err == nil || calls != 0 || strings.Contains(err.Error(), endpoint) || strings.Contains(err.Error(), "sentinel") {
				t.Fatalf("URL boundary failed: calls %d err %v", calls, err)
			}
		})
	}
}

func TestDownloadResponseBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		length   int64
		size     int64
		encoding string
		want     errx.Exit
	}{
		{"success", 200, "abc", 3, 3, "", errx.OK},
		{"empty", 200, "", 0, 0, "", errx.OK},
		{"redirect", 302, "", 0, 3, "", errx.PermissionDenied},
		{"forbidden", 403, "", 0, 3, "", errx.PermissionDenied},
		{"auth", 401, "", 0, 3, "", errx.Auth},
		{"missing", 404, "", 0, 3, "", errx.NotFound},
		{"server", 503, "", 0, 3, "", errx.Retryable},
		{"short", 200, "ab", -1, 3, "", errx.Conflict},
		{"long", 200, "abcde", -1, 3, "", errx.Conflict},
		{"declared mismatch", 200, "abc", 4, 3, "", errx.Conflict},
		{"encoded", 200, "abc", 3, 3, "gzip", errx.Conflict},
		{"too large", 200, "", 0, contract.MaxFileDownloadBytes + 1, "", errx.Usage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			c := &Client{httpClient: &http.Client{Transport: fileTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.String() != "https://files.slack.com/files-pri/T1-F1/a.mp4" || r.Header.Get("Authorization") != "Bearer sentinel" || r.Header.Get("Accept-Encoding") != "identity" {
					t.Error("bad file request")
				}
				if _, ok := r.Context().Deadline(); !ok {
					t.Error("no download deadline")
				}
				return &http.Response{StatusCode: tt.status, Header: http.Header{"Location": []string{"https://evil.test"}, "Content-Encoding": []string{tt.encoding}}, Body: io.NopCloser(strings.NewReader(tt.body)), ContentLength: tt.length, Request: r}, nil
			})}}
			var out bytes.Buffer
			result, err := c.DownloadFile(context.Background(), "sentinel", FileDetails{File: File{ID: "F1", Mode: "hosted", Size: tt.size}, downloadURL: "https://files.slack.com/files-pri/T1-F1/a.mp4"}, "T1", &out)
			if tt.want == errx.OK {
				if err != nil || result.Bytes != tt.size || len(result.SHA256) != 64 || out.String() != tt.body {
					t.Fatalf("bad success: %+v %v", result, err)
				}
			} else if err == nil || errx.ExitCode(err) != tt.want {
				t.Fatalf("bad error: %v", err)
			}
			if calls > 1 || int64(out.Len()) > tt.size+1 {
				t.Fatal("unbounded/retried download")
			}
		})
	}
}

func TestAttachmentProjectionInExactMessageRead(t *testing.T) {
	for _, thread := range []bool{false, true} {
		t.Run(map[bool]string{false: "history", true: "thread"}[thread], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				if q.Get("oldest") != "2.0" || q.Get("latest") != "2.0" || q.Get("inclusive") != "true" || q.Get("limit") != "1" || q.Get("channel") != "C1" {
					t.Error("exact message bounds missing")
				}
				if thread && q.Get("ts") != "1.0" {
					t.Error("thread parent missing")
				}
				_, _ = io.WriteString(w, `{"ok":true,"messages":[{"ts":"2.0","files":[{"id":"F1","title":"video","url_private":"hidden","preview":"hidden"}]}]}`)
			}))
			defer server.Close()
			c := newTestClient(server.URL, server.Client())
			var page MessagePage
			var err error
			if thread {
				page, err = c.Replies(context.Background(), "sentinel", ThreadOptions{ConversationID: "C1", ThreadTS: "1.0", Oldest: "2.0", Latest: "2.0", Inclusive: true, Limit: 1})
			} else {
				page, err = c.History(context.Background(), "sentinel", HistoryOptions{ConversationID: "C1", Oldest: "2.0", Latest: "2.0", Inclusive: true, Limit: 1})
			}
			if err != nil {
				t.Fatal(err)
			}
			b, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Messages[0].Files) != 1 || strings.Contains(string(b), "hidden") {
				t.Fatalf("bad projection %s", b)
			}
		})
	}
}

type failedFileWriter struct{}

func (failedFileWriter) Write([]byte) (int, error) {
	return 0, errors.New("private filesystem failure")
}

func TestDownloadFailureRedactionAndCancellation(t *testing.T) {
	for _, scenario := range []string{"transport", "writer", "cancelled", "external"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			c := &Client{httpClient: &http.Client{Transport: fileTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if scenario == "transport" {
					return nil, errors.New("sentinel https://private.example")
				}
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("abc")), ContentLength: 3, Request: r}, nil
			})}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			file := FileDetails{File: File{ID: "F1", Mode: "hosted", Size: 3, IsExternal: scenario == "external"}, downloadURL: "https://files.slack.com/files-pri/T1-F1/a"}
			var target io.Writer = io.Discard
			if scenario == "writer" {
				target = failedFileWriter{}
			}
			_, err := c.DownloadFile(ctx, "sentinel", file, "T1", target)
			if err == nil || strings.Contains(err.Error(), "sentinel") || strings.Contains(err.Error(), "private.example") || strings.Contains(err.Error(), "filesystem") || calls > 1 {
				t.Fatalf("bad failure %v", err)
			}
			if scenario == "external" && calls != 0 {
				t.Fatal("external file dispatched")
			}
		})
	}
}
