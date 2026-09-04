// Package cli wires the stable command contract to typed dependencies.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/abigotado/slack-agent-cli/internal/auth"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/skills"
	"github.com/abigotado/slack-agent-cli/internal/slack"
	"github.com/abigotado/slack-agent-cli/internal/writestate"
)

// AuthAPI is the one-method credential identity boundary.
type AuthAPI interface {
	AuthTest(context.Context, slack.Token) (slack.Identity, error)
}

// ConversationAPI owns discovery and exact-target preflight.
type ConversationAPI interface {
	ConversationsList(context.Context, slack.Token, slack.ListOptions) (slack.ConversationPage, error)
	ConversationInfo(context.Context, slack.Token, string) (slack.Conversation, error)
}

// MessageAPI owns bounded conversation-content reads.
type MessageAPI interface {
	History(context.Context, slack.Token, slack.HistoryOptions) (slack.MessagePage, error)
	Replies(context.Context, slack.Token, slack.ThreadOptions) (slack.MessagePage, error)
}

// UserAPI owns exact user lookup.
type UserAPI interface {
	UserInfo(context.Context, slack.Token, string) (slack.User, error)
}

// WriteAPI owns the sole one-attempt v1 remote mutation.
type WriteAPI interface {
	PostMessage(context.Context, slack.Token, slack.PostOptions) (slack.PostResult, error)
}

// Dependencies are explicit process boundaries.
type Dependencies struct {
	Profiles      *profile.Registry
	Policies      *policy.Registry
	Credentials   auth.Store
	Auth          AuthAPI
	Conversations ConversationAPI
	Messages      MessageAPI
	Users         UserAPI
	Writes        WriteAPI
	WriteState    *writestate.Tracker
	Input         io.Reader
	TokenTTY      func() (string, error)
	Output        *output.Writer
}

// DefaultDependencies resolves production local stores without performing I/O.
func DefaultDependencies() (Dependencies, error) {
	profiles, err := profile.DefaultRegistry()
	if err != nil {
		return Dependencies{}, err
	}
	policies, err := policy.DefaultRegistry()
	if err != nil {
		return Dependencies{}, err
	}
	client := slack.New()
	return Dependencies{
		Profiles: profiles, Policies: policies, Credentials: auth.KeychainStore{},
		Auth: client, Conversations: client, Messages: client, Users: client, Writes: client,
		WriteState: &writestate.Tracker{}, Input: os.Stdin, TokenTTY: auth.ReadTokenTTY, Output: output.New(),
	}, nil
}

// Run executes one invocation and returns its stable process status.
func Run(ctx context.Context, args []string, dependencies Dependencies) errx.Exit {
	root := newRoot(dependencies)
	if _, _, err := root.Find(args); err != nil {
		return dependencies.Output.Failure(usageError("UNKNOWN_COMMAND", "command is not part of the v1 surface", nil))
	}
	root.SetArgs(args)
	root.SetOut(dependencies.Output.Out)
	root.SetErr(dependencies.Output.Err)
	if err := root.ExecuteContext(ctx); err != nil {
		if output.IsConfirmedWriteOutputFailure(err) {
			return errx.Internal
		}
		return dependencies.Output.Failure(translate(err))
	}
	return errx.OK
}

func newRoot(dependencies Dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "slack-agent-cli",
		Short:         "Safe typed Slack boundary for agents",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          exactArgs(0),
		RunE:          subcommandRequired,
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetHelpCommand(&cobra.Command{Use: "help", Hidden: true, Args: cobra.ArbitraryArgs, RunE: func(*cobra.Command, []string) error {
		return usageError("HELP_FLAG_REQUIRED", "use --help for prose help", nil)
	}})
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError("INVALID_FLAGS", "invalid command flags", err)
	})
	root.AddCommand(
		newVersionCommand(dependencies),
		newContractCommand(dependencies),
		newAuthCommand(dependencies),
		newMeCommand(dependencies),
		newConversationsCommand(dependencies),
		newMessagesCommand(dependencies),
		newUsersCommand(dependencies),
		newSkillCommand(dependencies),
	)
	return root
}

func subcommandRequired(*cobra.Command, []string) error {
	return usageError("SUBCOMMAND_REQUIRED", "a leaf subcommand is required", nil)
}

func exactArgs(count int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != count {
			return usageError("INVALID_ARGUMENTS", fmt.Sprintf("expected %d positional arguments", count), nil)
		}
		return nil
	}
}

func requireProfile(name string) error {
	if name == "" {
		return errx.New(errx.Usage, "PROFILE_REQUIRED", "an explicit profile is required for every network command", "re-run with --profile NAME")
	}
	if err := profile.ValidateName(name); err != nil {
		return usageError("INVALID_PROFILE", "profile name is invalid", err)
	}
	return nil
}

func usageError(code, message string, cause error) error {
	value := errx.New(errx.Usage, code, message, "fix the command input and retry")
	if cause != nil {
		return value.Wrap(cause)
	}
	return value
}

func translate(err error) error {
	var typed *errx.Error
	if errors.As(err, &typed) {
		return err
	}
	switch {
	case errors.Is(err, profile.ErrNotFound):
		return errx.New(errx.NotFound, "PROFILE_NOT_FOUND", "profile was not found", "choose an exact name from auth list")
	case profile.WasCommitted(err):
		return errx.New(errx.Conflict, "PROFILE_COMMITTED_WITH_WARNING", "profile change committed with a durability warning", "inspect auth status before deciding whether to retry")
	case profile.RollbackFailed(err):
		return errx.New(errx.Conflict, "AUTH_TRANSACTION_INCONSISTENT", "credential rollback failed before profile commit", "inspect and repair the exact profile; do not retry a network operation")
	case errors.Is(err, auth.ErrNotFound):
		return errx.New(errx.Auth, "CREDENTIAL_NOT_FOUND", "profile credential was not found", "login the exact profile")
	case errors.Is(err, auth.ErrInteractionNotAllowed):
		return errx.New(errx.Auth, "KEYCHAIN_UI_BLOCKED", "Keychain access requires interaction", "unlock Keychain explicitly, then retry")
	case errors.Is(err, auth.ErrOverwriteConfirmationRequired):
		return errx.New(errx.ConfirmationRequired, "PROFILE_OVERWRITE_CONFIRMATION_REQUIRED", "profile or credential already exists", "review the exact profile, then add --yes")
	case errors.Is(err, auth.ErrBindingMismatch):
		return errx.New(errx.Conflict, "CREDENTIAL_BINDING_STALE", "credential binding no longer matches the selected profile", "inspect and login the exact profile; do not retry the network call unchanged")
	case errors.Is(err, policy.ErrNotAllowed):
		return errx.New(errx.PermissionDenied, "TARGET_NOT_ALLOWLISTED", "conversation is not allowlisted for this operation", "request an explicit policy change; do not broaden automatically")
	case errors.Is(err, policy.ErrBindingMismatch):
		return errx.New(errx.Conflict, "POLICY_BINDING_STALE", "policy does not match the current profile generation", "rebuild the exact policy after reviewing the profile")
	case errors.Is(err, policy.ErrRebindNotNeeded):
		return errx.New(errx.Conflict, "STALE_POLICY_REBIND_NOT_REQUIRED", "policy binding is already current", "inspect the current policy; do not retry the reset unchanged")
	case errors.Is(err, policy.ErrSharedMismatch):
		return errx.New(errx.Conflict, "TARGET_SHARED_STATE_CHANGED", "conversation shared state differs from policy", "review the target and rebuild policy explicitly")
	case errors.Is(err, policy.ErrWriteNeedsRead):
		return errx.New(errx.Conflict, "WRITE_POLICY_REQUIRES_READ", "write targets must remain a subset of read targets", "remove dependent write targets first")
	case policy.WasCommitted(err):
		return errx.New(errx.Conflict, "POLICY_COMMITTED_WITH_WARNING", "policy change committed with a durability warning", "inspect policy status before deciding whether to retry")
	case skills.WasCommitted(err):
		return errx.New(errx.Conflict, "SKILL_COMMITTED_WITH_WARNING", "Skill change committed with a durability or cleanup warning", "inspect the exact destination before deciding whether to retry")
	case errors.Is(err, skills.ErrConflict):
		return errx.New(errx.Conflict, "SKILL_INSTALL_CONFLICT", "Skill destination is modified or not owned", "review the destination; do not overwrite or remove it automatically")
	default:
		return errx.New(errx.Internal, "INTERNAL", "internal failure", "report this defect; do not retry unchanged").Wrap(err)
	}
}
