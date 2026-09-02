// Package output emits the strict v1 JSON envelope.
package output

import (
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
)

// Meta is stable non-secret invocation context.
type Meta struct {
	Profile      string `json:"profile,omitempty"`
	WorkspaceID  string `json:"workspace_id,omitempty"`
	ContentTrust string `json:"content_trust,omitempty"`
	Count        *int   `json:"count,omitempty"`
	NextCursor   string `json:"next_cursor,omitempty"`
	Reconciled   *bool  `json:"reconciled,omitempty"`
}

// ErrorBody is the failure half of the envelope.
type ErrorBody struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	RetryAfter int    `json:"retry_after_seconds,omitempty"`
}

// Envelope is the strict version-1 machine response.
type Envelope struct {
	OK    bool       `json:"ok"`
	V     int        `json:"v"`
	Data  any        `json:"data,omitempty"`
	Meta  *Meta      `json:"meta,omitempty"`
	Error *ErrorBody `json:"error,omitempty"`
	Hint  string     `json:"hint,omitempty"`
}

// Writer separates machine output from bounded diagnostics.
type Writer struct {
	Out io.Writer
	Err io.Writer
}

type confirmedWriteOutputError struct{ cause error }

func (e *confirmedWriteOutputError) Error() string { return "confirmed write output delivery failed" }
func (e *confirmedWriteOutputError) Unwrap() error { return e.cause }

// New creates a process writer.
func New() *Writer { return &Writer{Out: os.Stdout, Err: os.Stderr} }

// Success emits exactly one compact envelope.
func (w *Writer) Success(data any, meta *Meta) error {
	if data == nil {
		return errx.New(errx.Internal, "NIL_SUCCESS_DATA", "success data cannot be null", "report this defect")
	}
	return w.write(Envelope{OK: true, V: contract.EnvelopeVersion, Data: data, Meta: meta})
}

// ConfirmedWriteSuccess emits a verified remote-write result. If stdout fails,
// it emits the one exact emergency marker and prevents a second JSON attempt.
func (w *Writer) ConfirmedWriteSuccess(data any, meta *Meta) error {
	if data == nil {
		return errx.New(errx.Internal, "NIL_SUCCESS_DATA", "success data cannot be null", "report this defect")
	}
	if err := w.write(Envelope{OK: true, V: contract.EnvelopeVersion, Data: data, Meta: meta}); err != nil {
		w.diagnostic("SLACK_AGENT_CLI_CONFIRMED_WRITE_OUTPUT_FAILURE\n")
		return &confirmedWriteOutputError{cause: err}
	}
	return nil
}

// Failure emits exactly one compact failure envelope and returns its status.
func (w *Writer) Failure(failure error) errx.Exit {
	typed := errx.As(failure)
	body := &ErrorBody{Code: typed.Code, Message: typed.Message}
	if typed.RetryAfter > 0 {
		body.RetryAfter = int(typed.RetryAfter.Seconds())
	}
	if err := w.write(Envelope{OK: false, V: contract.EnvelopeVersion, Error: body, Hint: typed.Hint}); err != nil {
		w.diagnostic("SLACK_AGENT_CLI_OUTPUT_FAILURE\n")
	}
	return typed.Exit
}

func (w *Writer) write(value Envelope) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return errx.New(errx.Internal, "OUTPUT_ENCODE_FAILED", "failed to encode output", "report this defect").Wrap(err)
	}
	payload = append(payload, '\n')
	if len(payload) > contract.MaxStdoutBytes {
		return errx.New(errx.Internal, "OUTPUT_TOO_LARGE", "output exceeded the v1 bound", "narrow the request or report this defect")
	}
	written, err := w.Out.Write(payload)
	if err != nil {
		return errx.New(errx.Internal, "OUTPUT_WRITE_FAILED", "failed to write output", "report this defect").Wrap(err)
	}
	if written != len(payload) {
		return errx.New(errx.Internal, "OUTPUT_WRITE_FAILED", "failed to write complete output", "report this defect").Wrap(io.ErrShortWrite)
	}
	return nil
}

func (w *Writer) diagnostic(message string) {
	if len(message) > contract.MaxStderrBytes {
		message = message[:contract.MaxStderrBytes]
	}
	_, _ = io.WriteString(w.Err, message)
}

// ConfirmedWriteEmergency emits the sole non-JSON marker permitted after a
// remote write is known to have succeeded but normal output cannot be trusted.
func (w *Writer) ConfirmedWriteEmergency() {
	w.diagnostic("SLACK_AGENT_CLI_CONFIRMED_WRITE_OUTPUT_FAILURE\n")
}

// IsOutputFailure reports whether an error was caused by output delivery.
func IsOutputFailure(err error) bool {
	var typed *errx.Error
	return errors.As(err, &typed) && (typed.Code == "OUTPUT_WRITE_FAILED" || typed.Code == "OUTPUT_TOO_LARGE")
}

// IsConfirmedWriteOutputFailure suppresses a second stdout write after a
// remotely confirmed message was already committed.
func IsConfirmedWriteOutputFailure(err error) bool {
	var target *confirmedWriteOutputError
	return errors.As(err, &target)
}
