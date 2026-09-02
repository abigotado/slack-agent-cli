package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/abigotado/slack-agent-cli/internal/cli"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/writestate"
)

func main() { os.Exit(run()) }

func run() (status int) {
	writer := output.New()
	var writeState *writestate.Tracker
	defer func() {
		if recover() != nil {
			status = recoveredStatus(writer, writeState)
		}
	}()
	dependencies, err := cli.DefaultDependencies()
	if err != nil {
		return int(writer.Failure(err))
	}
	writeState = dependencies.WriteState
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return int(cli.Run(ctx, os.Args[1:], dependencies))
}

func recoveredStatus(writer *output.Writer, writeState *writestate.Tracker) int {
	switch writeState.State() {
	case writestate.Started:
		return int(writer.Failure(errx.New(errx.Conflict, "WRITE_OUTCOME_UNKNOWN", "process failed after message dispatch began", "reconcile with a bounded read; never retry automatically")))
	case writestate.Confirmed:
		writer.ConfirmedWriteEmergency()
		return int(errx.Internal)
	default:
		return int(writer.Failure(errx.New(errx.Internal, "PANIC", "internal failure", "report this defect; do not retry unchanged")))
	}
}
