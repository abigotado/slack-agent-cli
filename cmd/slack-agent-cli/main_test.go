package main

import (
	"bytes"
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
