// Package errx defines stable recovery-oriented process errors.
package errx

import (
	"errors"
	"fmt"
	"time"
)

// Exit identifies an internal recovery category. ProcessExit maps failures to
// the public process status without changing their safe JSON diagnostics.
type Exit int

// Stage is a fixed, non-secret diagnostic location. Recovery never depends on
// it.
type Stage string

const (
	OK Exit = iota
	Internal
	Usage
	NotFound
	Ambiguous
	Auth
	Retryable
	ConfirmationRequired
	PermissionDenied
	Conflict
)

// Valid reports whether the stage is part of the fixed diagnostic contract.
func (stage Stage) Valid() bool {
	switch stage {
	case StagePreDispatch, StageTransport, StageHTTPResponse, StageHTTPServer,
		StageRateLimitResponse, StageResponseBody, StageResponseJSON, StageAPIError:
		return true
	default:
		return false
	}
}

const (
	// StagePreDispatch means cancellation was observed before an HTTP dispatch.
	StagePreDispatch Stage = "pre_dispatch"
	// StageTransport means the HTTP transport did not return a response.
	StageTransport Stage = "transport"
	// StageHTTPResponse means Slack returned an unexpected non-server status.
	StageHTTPResponse Stage = "http_response"
	// StageHTTPServer means Slack returned a server-error status.
	StageHTTPServer Stage = "http_server"
	// StageRateLimitResponse means Slack returned an unusable rate-limit response.
	StageRateLimitResponse Stage = "rate_limit_response"
	// StageResponseBody means the bounded response body could not be read.
	StageResponseBody Stage = "response_body"
	// StageResponseJSON means the bounded response was not one valid JSON object.
	StageResponseJSON Stage = "response_json"
	// StageAPIError means Slack returned an API error outside the typed allowlist.
	StageAPIError Stage = "api_error"
)

// Error is safe for the machine envelope. Cause is diagnostic-only.
type Error struct {
	Exit       Exit
	Code       string
	Message    string
	Hint       string
	Stage      Stage
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.cause }

// Wrap preserves a low-level cause without exposing it in JSON output.
func (e *Error) Wrap(cause error) *Error {
	clone := *e
	clone.cause = cause
	return &clone
}

// WithStage adds a fixed, non-secret diagnostic stage without changing
// recovery semantics.
func (e *Error) WithStage(stage Stage) *Error {
	clone := *e
	clone.Stage = stage
	return &clone
}

// New constructs a typed safe error.
func New(exit Exit, code, message, hint string) *Error {
	return &Error{Exit: exit, Code: code, Message: message, Hint: hint}
}

// Newf constructs a typed safe error with a formatted safe message.
func Newf(exit Exit, code, hint, format string, args ...any) *Error {
	return New(exit, code, fmt.Sprintf(format, args...), hint)
}

// As returns the typed error, converting unknown failures to INTERNAL.
func As(err error) *Error {
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return New(Internal, "INTERNAL", "internal failure", "report this defect; do not retry unchanged").Wrap(err)
}

// ExitCode returns the internal recovery category for err.
func ExitCode(err error) Exit { return As(err).Exit }

// ProcessExit returns a nonzero public status for every failure. Remote API
// rejections and authentication failures share status 1; local recovery and
// ambiguous write outcomes retain their specific statuses.
func ProcessExit(err error) Exit {
	typed := As(err)
	if typed.Exit <= OK || typed.Exit > Conflict {
		return Internal
	}
	if typed.Exit == Conflict {
		return Conflict
	}
	if typed.Exit == Auth || typed.Exit == Retryable {
		return Internal
	}
	switch typed.Code {
	case "SLACK_OBJECT_NOT_FOUND", "SLACK_WRITE_TARGET_UNAVAILABLE",
		"SLACK_PERMISSION_DENIED", "SLACK_WRITE_PERMISSION_DENIED",
		"SLACK_WRITE_REJECTED", "FILE_REDIRECT_REJECTED":
		return Internal
	default:
		return typed.Exit
	}
}

// CodeInfo documents one public process status.
type CodeInfo struct {
	Exit     Exit   `json:"exit"`
	Name     string `json:"name"`
	Meaning  string `json:"meaning"`
	NextMove string `json:"next_move"`
}

var codeTable = []CodeInfo{
	{OK, "OK", "success", "proceed"},
	{Internal, "ERROR", "API, authentication, or internal failure", "inspect error.code and hint; never retry a write automatically"},
	{Usage, "USAGE", "invalid flags or bounded input", "fix input"},
	{NotFound, "NOT_FOUND", "local object or exact attachment absent", "verify exact ID and profile"},
	{Ambiguous, "AMBIGUOUS", "multiple objects matched", "choose an exact object"},
	{ConfirmationRequired, "CONFIRMATION_REQUIRED", "write approval missing", "review dry-run and confirm exact intent"},
	{PermissionDenied, "PERMISSION_DENIED", "local policy or capability denied operation", "request permission or explicit policy change"},
	{Conflict, "CONFLICT", "stale or unknown write state", "re-read and reconcile; never retry automatically"},
}

// Codes returns a copy of the public process exit contract.
func Codes() []CodeInfo { return append([]CodeInfo(nil), codeTable...) }
