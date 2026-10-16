package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/writestate"
)

func TestRecoveredStatusPreservesUnknownWrite(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	tracker := &writestate.Tracker{}
	tracker.MarkStarted()
	status := recoveredStatus(&output.Writer{Out: &stdout, Err: &stderr}, tracker)
	if status != int(errx.Conflict) || !strings.Contains(stdout.String(), `"code":"WRITE_OUTCOME_UNKNOWN"`) {
		t.Fatalf("status=%d stdout=%q", status, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestRecoveredStatusAfterConfirmedWriteUsesOnlyEmergencyMarker(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	tracker := &writestate.Tracker{}
	tracker.MarkConfirmed()
	status := recoveredStatus(&output.Writer{Out: &stdout, Err: &stderr}, tracker)
	if status != int(errx.Internal) || stdout.Len() != 0 {
		t.Fatalf("status=%d stdout=%q", status, stdout.String())
	}
	if stderr.String() != "SLACK_AGENT_CLI_CONFIRMED_WRITE_OUTPUT_FAILURE\n" {
		t.Fatalf("stderr=%q", stderr.String())
	}
}

func TestBinaryExitStatusMatchesJSON(t *testing.T) {
	t.Parallel()
	binary := filepath.Join(t.TempDir(), "slack-agent-cli")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}
	for _, test := range []struct {
		name string
		args []string
		exit int
		code string
	}{
		{"success", []string{"version"}, 0, ""},
		{"thread input", []string{"messages", "thread", "--limit", "0"}, 2, "INVALID_LIMIT"},
		{"history input", []string{"messages", "history", "--limit", "101"}, 2, "INVALID_LIMIT"},
		{"list input", []string{"conversations", "list", "--limit", "-1"}, 2, "INVALID_LIMIT"},
		{"invalid flag", []string{"version", "--unknown"}, 2, "INVALID_FLAGS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(binary, test.args...)
			command.Env = append(os.Environ(), "HOME="+t.TempDir())
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			status := 0
			if err != nil {
				var failure *exec.ExitError
				if !errors.As(err, &failure) {
					t.Fatal(err)
				}
				status = failure.ExitCode()
			}
			if status != test.exit || stderr.Len() != 0 {
				t.Fatalf("exit=%d want=%d stdout=%s stderr=%s", status, test.exit, &stdout, &stderr)
			}
			var envelope output.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatalf("decode stdout: %v output=%q", err, stdout.String())
			}
			if envelope.V != 1 || envelope.OK != (status == 0) || (test.code != "" && (envelope.Error == nil || envelope.Error.Code != test.code)) {
				t.Fatalf("exit and envelope disagree: %s", &stdout)
			}
		})
	}
}
