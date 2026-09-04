package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/intent"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

func newSendCommand(dependencies Dependencies) *cobra.Command {
	var name, conversationID, threadTS, confirmation string
	var textStdin, dryRun, yes bool
	command := &cobra.Command{
		Use: "send", Args: exactArgs(0),
		RunE: func(command *cobra.Command, _ []string) error {
			options := sendOptions{
				profile: name, conversationID: conversationID, threadTS: threadTS,
				confirmation: confirmation, textStdin: textStdin, dryRun: dryRun, yes: yes,
			}
			return executeSend(command.Context(), dependencies, options)
		},
	}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().StringVar(&conversationID, "conversation-id", "", "exact conversation ID")
	command.Flags().StringVar(&threadTS, "thread-ts", "", "optional exact parent Slack timestamp")
	command.Flags().BoolVar(&textStdin, "text-stdin", false, "read bounded plain text from stdin")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "produce a local intent receipt without Keychain or network")
	command.Flags().StringVar(&confirmation, "confirm-intent", "", "exact SHA-256 from dry-run receipt")
	command.Flags().BoolVar(&yes, "yes", false, "confirm this exact intent")
	return command
}

type sendOptions struct {
	profile        string
	conversationID string
	threadTS       string
	confirmation   string
	textStdin      bool
	dryRun         bool
	yes            bool
}

func executeSend(ctx context.Context, dependencies Dependencies, options sendOptions) error {
	if err := requireProfile(options.profile); err != nil {
		return err
	}
	if !options.textStdin {
		return usageError("TEXT_STDIN_REQUIRED", "--text-stdin is required", nil)
	}
	if options.threadTS != "" {
		if err := validateTimestamp(options.threadTS, "--thread-ts"); err != nil {
			return err
		}
	}
	if options.dryRun && (options.yes || options.confirmation != "") {
		return usageError("DRY_RUN_CONFLICT", "--dry-run cannot be combined with confirmation flags", nil)
	}
	p, err := dependencies.Profiles.Get(ctx, options.profile)
	if err != nil {
		return err
	}
	if !p.HasCapability(profile.CapabilityMessageWrite) {
		return errxPermission("CAPABILITY_NOT_DECLARED", "profile does not declare message-write capability")
	}
	if _, err := dependencies.Policies.Target(ctx, p, policy.Write, options.conversationID); err != nil {
		return err
	}
	text, err := readMessageText(dependencies.Input)
	if err != nil {
		return usageError("INVALID_MESSAGE_TEXT", "message text is invalid", err)
	}
	receipt, err := intent.New(p, options.conversationID, options.threadTS, text)
	if err != nil {
		return usageError("INVALID_MESSAGE_TEXT", "message text is invalid", err)
	}
	if options.dryRun {
		return dependencies.Output.Success(receipt, &output.Meta{Profile: p.Name, WorkspaceID: p.WorkspaceID})
	}
	return executeConfirmedSend(ctx, dependencies, options, p, text, receipt)
}

func executeConfirmedSend(ctx context.Context, dependencies Dependencies, options sendOptions, p profile.Profile, text string, receipt intent.Receipt) error {
	if !options.yes || options.confirmation == "" {
		return errx.New(errx.ConfirmationRequired, "MESSAGE_CONFIRMATION_REQUIRED", "message send requires the exact dry-run intent", "review the receipt, then pass --confirm-intent SHA256 --yes")
	}
	if options.confirmation != receipt.IntentSHA256 {
		return errx.New(errx.Conflict, "INTENT_MISMATCH", "confirmation does not match the current message intent", "repeat dry-run and review the new receipt")
	}
	current, err := loadSession(ctx, dependencies, options.profile, profile.CapabilityMessageWrite)
	if err != nil {
		return err
	}
	if profile.Identity(current.profile) != receipt.ProfileIdentity || current.profile.CredentialGeneration != receipt.Generation {
		return errx.New(errx.Conflict, "PROFILE_CHANGED", "profile changed after dry-run", "repeat dry-run and review the new identity")
	}
	identity, err := dependencies.Auth.AuthTest(ctx, current.token)
	if err != nil {
		return err
	}
	if !identityMatchesProfile(p, identity) {
		return credentialIdentityChanged()
	}
	if _, _, err := preflightTarget(ctx, dependencies, current, options.conversationID, policy.Write); err != nil {
		return err
	}
	if err := preflightThread(ctx, dependencies, current, options); err != nil {
		return err
	}
	result, reconciled, err := dispatchMessage(ctx, dependencies, current, options, text)
	if err != nil {
		return err
	}
	dependencies.WriteState.MarkConfirmed()
	applied := map[string]any{"conversation_id": result.ConversationID, "ts": result.TS, "thread_ts": result.ThreadTS, "intent_sha256": receipt.IntentSHA256, "applied": true}
	return dependencies.Output.ConfirmedWriteSuccess(applied, &output.Meta{Profile: p.Name, WorkspaceID: p.WorkspaceID, Reconciled: &reconciled})
}

func preflightThread(ctx context.Context, dependencies Dependencies, current session, options sendOptions) error {
	if options.threadTS == "" {
		return nil
	}
	page, err := dependencies.Messages.Replies(ctx, current.token, slack.ThreadOptions{ConversationID: options.conversationID, ThreadTS: options.threadTS, Limit: 1})
	if err != nil {
		return err
	}
	if len(page.Messages) == 0 || page.Messages[0].TS != options.threadTS {
		return errx.New(errx.NotFound, "THREAD_NOT_FOUND", "parent thread was not found exactly", "verify the exact thread timestamp")
	}
	return nil
}

func dispatchMessage(ctx context.Context, dependencies Dependencies, current session, options sendOptions, text string) (slack.PostResult, bool, error) {
	dispatchedAt := time.Now()
	writeContext, cancel := context.WithTimeout(ctx, contract.WriteDeadline)
	defer cancel()
	dependencies.WriteState.MarkStarted()
	result, writeErr := dependencies.Writes.PostMessage(writeContext, current.token, slack.PostOptions{ConversationID: options.conversationID, ThreadTS: options.threadTS, Text: text})
	if writeErr == nil && (result.ConversationID != options.conversationID || validateTimestamp(result.TS, "Slack response timestamp") != nil) {
		writeErr = errx.New(errx.Conflict, "WRITE_OUTCOME_UNKNOWN", "Slack write response identity was incomplete", "reconcile with a bounded read; never retry automatically")
	}
	if !isUnknownWrite(writeErr) {
		return result, false, writeErr
	}
	match, ok := reconcileMessage(ctx, dependencies, current, options, text, dispatchedAt)
	if !ok {
		return slack.PostResult{}, false, writeErr
	}
	return match, true, nil
}

func readMessageText(reader io.Reader) (string, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, contract.MaxMessageBytes+1))
	if err != nil {
		return "", fmt.Errorf("read message: %w", err)
	}
	if len(payload) > contract.MaxMessageBytes {
		return "", errors.New("message exceeds byte bound")
	}
	return string(payload), nil
}
func isUnknownWrite(err error) bool {
	if err == nil {
		return false
	}
	var typed *errx.Error
	return errors.As(err, &typed) && typed.Code == "WRITE_OUTCOME_UNKNOWN"
}

func reconcileMessage(ctx context.Context, dependencies Dependencies, current session, options sendOptions, text string, dispatchedAt time.Time) (slack.PostResult, bool) {
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), contract.ReconcileDeadline)
	defer cancel()
	if _, _, err := preflightTarget(reconcileContext, dependencies, current, options.conversationID, policy.Read); err != nil {
		return slack.PostResult{}, false
	}
	messages, ok := reconciliationMessages(reconcileContext, dependencies, current, options, dispatchedAt)
	if !ok {
		return slack.PostResult{}, false
	}
	matches := make([]slack.Message, 0, 1)
	digest := intent.DigestText(text)
	for _, message := range messages {
		authorMatch := message.User == current.profile.UserID || (current.profile.BotID != "" && message.BotID == current.profile.BotID)
		threadMatch := message.ThreadTS == options.threadTS
		if options.threadTS == "" {
			threadMatch = message.ThreadTS == ""
		}
		if authorMatch && threadMatch && intent.DigestText(message.Text) == digest {
			matches = append(matches, message)
		}
	}
	if len(matches) != 1 {
		return slack.PostResult{}, false
	}
	return slack.PostResult{ConversationID: options.conversationID, TS: matches[0].TS, ThreadTS: options.threadTS}, true
}

func reconciliationMessages(ctx context.Context, dependencies Dependencies, current session, options sendOptions, dispatchedAt time.Time) ([]slack.Message, bool) {
	if options.threadTS == "" {
		oldest := strconv.FormatInt(dispatchedAt.Add(-2*time.Minute).Unix(), 10)
		latest := strconv.FormatInt(dispatchedAt.Add(2*time.Minute).Unix(), 10)
		page, err := dependencies.Messages.History(ctx, current.token, slack.HistoryOptions{ConversationID: options.conversationID, Limit: contract.MaxCollectionLimit, Oldest: oldest, Latest: latest})
		if err != nil {
			return nil, false
		}
		return page.Messages, true
	}
	page, err := dependencies.Messages.Replies(ctx, current.token, slack.ThreadOptions{ConversationID: options.conversationID, ThreadTS: options.threadTS, Limit: contract.MaxCollectionLimit})
	if err != nil {
		return nil, false
	}
	return page.Messages, true
}
