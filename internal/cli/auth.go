package cli

import (
	"context"
	"errors"
	"sort"

	"github.com/spf13/cobra"

	"github.com/abigotado/slack-agent-cli/internal/auth"
	"github.com/abigotado/slack-agent-cli/internal/contract"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

func newAuthCommand(dependencies Dependencies) *cobra.Command {
	command := &cobra.Command{Use: "auth", Args: exactArgs(0), RunE: subcommandRequired}
	command.AddCommand(newAuthLoginCommand(dependencies), newAuthListCommand(dependencies), newAuthStatusCommand(dependencies), newAuthLogoutCommand(dependencies), newPolicyGroup(dependencies, policy.Read), newPolicyGroup(dependencies, policy.Write))
	return command
}

func newAuthLoginCommand(dependencies Dependencies) *cobra.Command {
	var name, tokenKind string
	var capabilities []string
	var tokenStdin, tokenTTY, yes bool
	command := &cobra.Command{Use: "login", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := requireProfile(name); err != nil {
			return err
		}
		if !tokenStdin && !tokenTTY {
			return usageError("TOKEN_STDIN_REQUIRED", "--token-stdin is required", nil)
		}
		if tokenStdin && tokenTTY {
			return usageError("TOKEN_INPUT_CONFLICT", "choose exactly one of --token-stdin or --token-tty", nil)
		}
		kind := profile.TokenKind(tokenKind)
		if kind != profile.TokenUser && kind != profile.TokenBot {
			return usageError("INVALID_TOKEN_KIND", "--token-kind must be user or bot", nil)
		}
		parsedCapabilities, err := parseCapabilities(capabilities)
		if err != nil {
			return err
		}
		var token string
		if tokenTTY {
			if dependencies.TokenTTY == nil {
				return usageError("TOKEN_TTY_UNAVAILABLE", "a controlling terminal is required for --token-tty", nil)
			}
			token, err = dependencies.TokenTTY()
			switch {
			case errors.Is(err, auth.ErrTTYUnavailable):
				return usageError("TOKEN_TTY_UNAVAILABLE", "a controlling terminal is required for --token-tty", err)
			case errors.Is(err, auth.ErrTTYRestore):
				return errx.New(errx.Internal, "TOKEN_TTY_RECOVERY_REQUIRED", "terminal echo restoration failed", "run `stty echo` in that terminal before retrying")
			case errors.Is(err, auth.ErrTTYInterrupted):
				return usageError("TOKEN_TTY_INTERRUPTED", "terminal token input was interrupted", err)
			case errors.Is(err, auth.ErrTTYIO):
				return errx.New(errx.Internal, "TOKEN_TTY_IO_FAILED", "controlling terminal input failed", "verify the terminal, then retry the login")
			}
		} else {
			token, err = auth.ReadToken(dependencies.Input)
		}
		if err == nil {
			token, err = auth.ValidateToken(token)
		}
		if err != nil {
			return usageError("INVALID_TOKEN_INPUT", "token input is invalid", err)
		}
		identity, err := dependencies.Auth.AuthTest(command.Context(), slack.Token(token))
		if err != nil {
			return err
		}
		actualKind := profile.TokenUser
		if identity.BotID != "" {
			actualKind = profile.TokenBot
		}
		if actualKind != kind {
			return errx.New(errx.Auth, "TOKEN_KIND_MISMATCH", "token identity does not match --token-kind", "choose the verified token kind")
		}
		verified := profile.Profile{Name: name, WorkspaceID: identity.WorkspaceID, WorkspaceName: identity.WorkspaceName, WorkspaceURL: profile.NormalizeURL(identity.WorkspaceURL), UserID: identity.UserID, BotID: identity.BotID, EnterpriseID: identity.EnterpriseID, TokenKind: kind, Capabilities: parsedCapabilities}
		stored, err := auth.Login(command.Context(), dependencies.Credentials, dependencies.Profiles, verified, token, yes)
		if err != nil {
			return err
		}
		return dependencies.Output.Success(stored, &output.Meta{Profile: stored.Name, WorkspaceID: stored.WorkspaceID, ContentTrust: "untrusted"})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().StringVar(&tokenKind, "token-kind", "", "user or bot")
	command.Flags().StringSliceVar(&capabilities, "capability", nil, "declared capability: read or message-write")
	command.Flags().BoolVar(&tokenStdin, "token-stdin", false, "read one bounded token line from stdin")
	command.Flags().BoolVar(&tokenTTY, "token-tty", false, "read one bounded hidden token line from the controlling terminal")
	command.Flags().BoolVar(&yes, "yes", false, "confirm exact profile overwrite")
	return command
}

func newAuthListCommand(dependencies Dependencies) *cobra.Command {
	return &cobra.Command{Use: "list", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		values, err := dependencies.Profiles.List(command.Context())
		if err != nil {
			return err
		}
		count := len(values)
		return dependencies.Output.Success(values, &output.Meta{Count: &count, ContentTrust: "untrusted"})
	}}
}

func newAuthStatusCommand(dependencies Dependencies) *cobra.Command {
	var name string
	var check bool
	command := &cobra.Command{Use: "status", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := requireProfile(name); err != nil {
			return err
		}
		value, err := dependencies.Profiles.Get(command.Context(), name)
		if err != nil {
			return err
		}
		data := map[string]any{"profile": value, "credential_checked": false}
		if check {
			current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
			if err != nil {
				return err
			}
			identity, err := dependencies.Auth.AuthTest(command.Context(), current.token)
			if err != nil {
				return err
			}
			if !identityMatchesProfile(value, identity) {
				return credentialIdentityChanged()
			}
			data["credential_checked"] = true
		}
		return dependencies.Output.Success(data, &output.Meta{Profile: name, WorkspaceID: value.WorkspaceID, ContentTrust: "untrusted"})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().BoolVar(&check, "check", false, "verify Keychain credential against Slack")
	return command
}

func newAuthLogoutCommand(dependencies Dependencies) *cobra.Command {
	var name string
	var yes bool
	command := &cobra.Command{Use: "logout", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := requireProfile(name); err != nil {
			return err
		}
		if !yes {
			return errx.New(errx.ConfirmationRequired, "LOGOUT_CONFIRMATION_REQUIRED", "logout removes the exact profile credential", "review the profile, then add --yes")
		}
		if err := auth.Logout(command.Context(), dependencies.Credentials, dependencies.Profiles, name); err != nil {
			return err
		}
		return dependencies.Output.Success(map[string]any{"profile": name, "removed": true}, nil)
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().BoolVar(&yes, "yes", false, "confirm logout")
	return command
}

func parseCapabilities(values []string) ([]profile.Capability, error) {
	if len(values) == 0 {
		return nil, usageError("CAPABILITY_REQUIRED", "at least --capability read is required", nil)
	}
	seen := map[profile.Capability]bool{}
	result := make([]profile.Capability, 0, len(values))
	for _, value := range values {
		capability := profile.Capability(value)
		if capability != profile.CapabilityRead && capability != profile.CapabilityMessageWrite {
			return nil, usageError("INVALID_CAPABILITY", "capability must be read or message-write", nil)
		}
		if seen[capability] {
			return nil, usageError("DUPLICATE_CAPABILITY", "capabilities must be unique", nil)
		}
		seen[capability] = true
		result = append(result, capability)
	}
	if !seen[profile.CapabilityRead] {
		return nil, usageError("READ_CAPABILITY_REQUIRED", "read capability is required", nil)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}

func newPolicyGroup(dependencies Dependencies, kind policy.Kind) *cobra.Command {
	name := "allow-reads"
	if kind == policy.Write {
		name = "allow-writes"
	}
	command := &cobra.Command{Use: name, Args: exactArgs(0), RunE: subcommandRequired}
	command.AddCommand(newPolicyShow(dependencies, kind), newPolicySet(dependencies, kind), newPolicyClear(dependencies, kind))
	return command
}
func newPolicyShow(dependencies Dependencies, kind policy.Kind) *cobra.Command {
	var name string
	command := &cobra.Command{Use: "show", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := requireProfile(name); err != nil {
			return err
		}
		p, err := dependencies.Profiles.Get(command.Context(), name)
		if err != nil {
			return err
		}
		set, err := dependencies.Policies.Get(command.Context(), p)
		if err != nil {
			return err
		}
		targets := set.Reads
		if kind == policy.Write {
			targets = set.Writes
		}
		count := len(targets)
		return dependencies.Output.Success(targets, &output.Meta{Profile: name, WorkspaceID: p.WorkspaceID, Count: &count})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	return command
}
func newPolicySet(dependencies Dependencies, kind policy.Kind) *cobra.Command {
	var name string
	var ids []string
	var allowShared, resetStalePolicy, dryRun, yes bool
	command := &cobra.Command{Use: "set", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		return executePolicySet(command.Context(), dependencies, name, kind, ids, allowShared, resetStalePolicy, dryRun, yes)
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().StringSliceVar(&ids, "conversation-id", nil, "exact conversation ID (repeatable)")
	command.Flags().BoolVar(&allowShared, "allow-slack-connect", false, "explicitly allow shared/external conversations")
	command.Flags().BoolVar(&resetStalePolicy, "reset-stale-policy", false, "replace a stale read policy and discard its old write targets")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "validate without changing policy")
	command.Flags().BoolVar(&yes, "yes", false, "apply exact policy change")
	return command
}
func newPolicyClear(dependencies Dependencies, kind policy.Kind) *cobra.Command {
	var name string
	var dryRun, yes bool
	command := &cobra.Command{Use: "clear", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := requireProfile(name); err != nil {
			return err
		}
		if dryRun == yes {
			return usageError("POLICY_CONFIRMATION_REQUIRED", "choose exactly one of --dry-run or --yes", nil)
		}
		p, err := dependencies.Profiles.Get(command.Context(), name)
		if err != nil {
			return err
		}
		if dryRun {
			preview, err := dependencies.Policies.PreviewReplace(command.Context(), p, kind, nil)
			if err != nil {
				return err
			}
			return dependencies.Output.Success(map[string]any{"policy": preview, "applied": false}, &output.Meta{Profile: name, WorkspaceID: p.WorkspaceID})
		}
		set, err := dependencies.Policies.Replace(command.Context(), p, kind, nil)
		if err != nil {
			return err
		}
		return dependencies.Output.Success(map[string]any{"policy": set, "applied": true}, &output.Meta{Profile: name, WorkspaceID: p.WorkspaceID})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "validate without change")
	command.Flags().BoolVar(&yes, "yes", false, "apply exact change")
	return command
}
func executePolicySet(ctx context.Context, dependencies Dependencies, name string, kind policy.Kind, ids []string, allowShared, resetStalePolicy, dryRun, yes bool) error {
	if err := requireProfile(name); err != nil {
		return err
	}
	if len(ids) == 0 {
		return usageError("POLICY_TARGET_REQUIRED", "at least one --conversation-id is required", nil)
	}
	if len(ids) > contract.MaxPolicyTargets {
		return usageError("POLICY_TARGET_LIMIT_EXCEEDED", "policy target count exceeds the v1 bound", nil)
	}
	if dryRun == yes {
		return usageError("POLICY_CONFIRMATION_REQUIRED", "choose exactly one of --dry-run or --yes", nil)
	}
	if resetStalePolicy && kind != policy.Read {
		return usageError("STALE_POLICY_REBIND_READ_ONLY", "--reset-stale-policy is valid only for allow-reads set", nil)
	}
	capability := profile.CapabilityRead
	if kind == policy.Write {
		capability = profile.CapabilityMessageWrite
	}
	current, err := loadSession(ctx, dependencies, name, capability)
	if err != nil {
		return err
	}
	if resetStalePolicy {
		if _, err := dependencies.Policies.Get(ctx, current.profile); err == nil {
			return usageError("STALE_POLICY_REBIND_NOT_REQUIRED", "policy binding is already current", nil)
		} else if !errors.Is(err, policy.ErrBindingMismatch) {
			return err
		}
	}
	targets := make([]policy.Target, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if err := profile.ValidateSlackID(id); err != nil {
			return usageError("INVALID_CONVERSATION_ID", "conversation ID is invalid", err)
		}
		if seen[id] {
			return usageError("DUPLICATE_CONVERSATION_ID", "conversation IDs must be unique", nil)
		}
		seen[id] = true
		conversation, err := dependencies.Conversations.ConversationInfo(ctx, current.token, id)
		if err != nil {
			return err
		}
		if conversation.ID != id {
			return errors.New("slack returned a different conversation identity")
		}
		shared, err := classifyTarget(ctx, dependencies, current, conversation)
		if err != nil {
			return err
		}
		if (shared.Shared || shared.ExternallyShared || shared.OrgShared) && !allowShared {
			return errxPermission("SLACK_CONNECT_OPT_IN_REQUIRED", "shared conversations require --allow-slack-connect")
		}
		targets = append(targets, policy.Target{ConversationID: id, Shared: shared})
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].ConversationID < targets[j].ConversationID })
	if dryRun {
		var preview policy.Set
		if resetStalePolicy {
			preview, err = dependencies.Policies.PreviewRebindRead(ctx, current.profile, targets)
		} else {
			preview, err = dependencies.Policies.PreviewReplace(ctx, current.profile, kind, targets)
		}
		if err != nil {
			return err
		}
		return dependencies.Output.Success(map[string]any{"policy": preview, "applied": false, "remote_checks": "performed"}, &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID})
	}
	var set policy.Set
	if resetStalePolicy {
		set, err = dependencies.Policies.RebindRead(ctx, current.profile, targets)
	} else {
		set, err = dependencies.Policies.Replace(ctx, current.profile, kind, targets)
	}
	if err != nil {
		return err
	}
	return dependencies.Output.Success(map[string]any{"policy": set, "applied": true}, &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID})
}
