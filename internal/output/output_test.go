package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/errx"
)

func TestStrictSuccessAndFailureBranches(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(*Writer)
		ok   bool
	}{
		{"success", func(w *Writer) {
			if err := w.Success(map[string]any{"id": "C1"}, &Meta{Profile: "work"}); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"failure", func(w *Writer) {
			if code := w.Failure(errx.New(errx.Usage, "BAD", "bad input", "fix it")); code != errx.Usage {
				t.Fatalf("exit %d", code)
			}
		}, false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			writer := &Writer{Out: &stdout, Err: &stderr}
			test.run(writer)
			var env map[string]any
			if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &env); err != nil {
				t.Fatal(err)
			}
			if env["v"] != float64(1) || env["ok"] != test.ok {
				t.Fatalf("bad envelope: %s", stdout.String())
			}
			_, hasData := env["data"]
			_, hasError := env["error"]
			if hasData == hasError {
				t.Fatalf("branches overlap: %s", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("unexpected stderr: %q", stderr.String())
			}
		})
	}
}

func TestSecretNeverAppearsInUnknownFailure(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	writer := &Writer{Out: &stdout, Err: &bytes.Buffer{}}
	writer.Failure(errors.New("xoxb-secret"))
	if strings.Contains(stdout.String(), "xoxb-secret") {
		t.Fatal("secret leaked")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestConfirmedWriteOutputFailureUsesExactMarker(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	writer := &Writer{Out: failingWriter{}, Err: &stderr}
	err := writer.ConfirmedWriteSuccess(map[string]any{"ts": "1.0"}, nil)
	if !IsConfirmedWriteOutputFailure(err) {
		t.Fatalf("unexpected error %v", err)
	}
	if stderr.String() != "SLACK_AGENT_CLI_CONFIRMED_WRITE_OUTPUT_FAILURE\n" {
		t.Fatalf("marker %q", stderr.String())
	}
}
