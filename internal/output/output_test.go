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

func TestFailureIncludesFixedDiagnosticStage(t *testing.T) {
	t.Parallel()
	const secret = "xoxb-secret"
	var stdout bytes.Buffer
	writer := &Writer{Out: &stdout, Err: &bytes.Buffer{}}
	failure := errx.New(errx.Retryable, "SLACK_READ_FAILED", "Slack read failed safely", "retry the bounded read with backoff").WithStage(errx.StageTransport).Wrap(errors.New(secret))
	if exit := writer.Failure(failure); exit != errx.Internal {
		t.Fatalf("exit %d", exit)
	}
	var envelope Envelope
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || envelope.Error.Stage != "transport" {
		t.Fatalf("unexpected envelope: %s", stdout.String())
	}
	if strings.Contains(stdout.String(), secret) {
		t.Fatal("wrapped cause leaked")
	}
}

func TestFailureSuppressesUnknownDiagnosticStage(t *testing.T) {
	t.Parallel()
	const secret = "xoxb-secret"
	var stdout bytes.Buffer
	writer := &Writer{Out: &stdout, Err: &bytes.Buffer{}}
	failure := errx.New(errx.Retryable, "SLACK_READ_FAILED", "Slack read failed safely", "retry the bounded read with backoff").WithStage(errx.Stage("transport/" + secret))
	writer.Failure(failure)
	if strings.Contains(stdout.String(), secret) {
		t.Fatal("unknown diagnostic stage leaked")
	}
	var envelope Envelope
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error == nil || envelope.Error.Stage != "" {
		t.Fatalf("unexpected envelope: %s", stdout.String())
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

func TestEveryFailureHasNonzeroProcessStatusWithoutChangingEnvelope(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		exit errx.Exit
		code string
		want errx.Exit
	}{
		{"accidental success", errx.OK, "BAD", errx.Internal},
		{"invalid negative", errx.Exit(-1), "BAD", errx.Internal},
		{"invalid positive", errx.Exit(256), "BAD", errx.Internal},
		{"internal", errx.Internal, "INTERNAL", errx.Internal},
		{"input", errx.Usage, "INVALID_LIMIT", errx.Usage},
		{"local missing", errx.NotFound, "PROFILE_NOT_FOUND", errx.NotFound},
		{"ambiguous", errx.Ambiguous, "AMBIGUOUS", errx.Ambiguous},
		{"auth", errx.Auth, "CREDENTIAL_NOT_FOUND", errx.Internal},
		{"retryable", errx.Retryable, "SLACK_READ_FAILED", errx.Internal},
		{"approval", errx.ConfirmationRequired, "MESSAGE_CONFIRMATION_REQUIRED", errx.ConfirmationRequired},
		{"policy", errx.PermissionDenied, "TARGET_NOT_ALLOWLISTED", errx.PermissionDenied},
		{"Slack Connect policy", errx.PermissionDenied, "SLACK_CONNECT_OPT_IN_REQUIRED", errx.PermissionDenied},
		{"remote missing", errx.NotFound, "SLACK_OBJECT_NOT_FOUND", errx.Internal},
		{"remote write target", errx.NotFound, "SLACK_WRITE_TARGET_UNAVAILABLE", errx.Internal},
		{"remote permission", errx.PermissionDenied, "SLACK_PERMISSION_DENIED", errx.Internal},
		{"remote write permission", errx.PermissionDenied, "SLACK_WRITE_PERMISSION_DENIED", errx.Internal},
		{"remote write input", errx.Usage, "SLACK_WRITE_REJECTED", errx.Internal},
		{"remote file redirect", errx.PermissionDenied, "FILE_REDIRECT_REJECTED", errx.Internal},
		{"unsupported local file", errx.PermissionDenied, "FILE_DOWNLOAD_UNSUPPORTED", errx.PermissionDenied},
		{"conflict", errx.Conflict, "POLICY_BINDING_STALE", errx.Conflict},
		{"unknown write", errx.Conflict, "WRITE_OUTCOME_UNKNOWN", errx.Conflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			writer := &Writer{Out: &stdout, Err: &stderr}
			failure := errx.New(test.exit, test.code, "safe message", "safe hint").Wrap(errors.New("credential-sentinel"))
			if exit := writer.Failure(failure); exit != test.want || exit == errx.OK {
				t.Fatalf("exit=%d want=%d", exit, test.want)
			}
			want := `{"ok":false,"v":1,"error":{"code":"` + test.code + `","message":"safe message"},"hint":"safe hint"}` + "\n"
			if stdout.String() != want || stderr.Len() != 0 {
				t.Fatalf("envelope changed: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			if errx.ExitCode(failure) != test.exit {
				t.Fatal("process mapping changed internal recovery category")
			}
		})
	}
}
