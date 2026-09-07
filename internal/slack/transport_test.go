package slack

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/abigotado/slack-agent-cli/internal/errx"
)

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error { b.closed = true; return nil }

func TestSharedDispatchClosesFailuresAndPreservesDeadlines(t *testing.T) {
	for _, class := range []operationClass{readOperation, downloadOperation, writeOperation} {
		for _, status := range []int{200, 302, 401, 403, 429, 500} {
			body := &trackedBody{Reader: strings.NewReader("")}
			calls := 0
			client := &Client{httpClient: &http.Client{Timeout: 15 * time.Second, Transport: fileTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				deadline, ok := r.Context().Deadline()
				remaining := time.Until(deadline)
				if !ok || (class != downloadOperation && (remaining < 14*time.Second || remaining > 15*time.Second)) || (class == downloadOperation && remaining < 110*time.Second) {
					t.Errorf("deadline=%v class=%v", remaining, class)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"1"}}, Body: body}, nil
			})}}
			request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://files.slack.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.do(request.Context(), "sentinel", request, class)
			if calls != 1 {
				t.Fatal("dispatch retried")
			}
			if status == 200 {
				if err != nil || body.closed {
					t.Fatalf("success ownership: %v", err)
				}
				response.Body.Close()
			} else {
				if err == nil || !body.closed {
					t.Fatalf("status=%d err=%v closed=%v", status, err, body.closed)
				}
				if status == 500 && errx.As(err).Stage != errx.StageHTTPServer {
					t.Fatalf("wrong stage: %+v", errx.As(err))
				}
			}
		}
	}
}
