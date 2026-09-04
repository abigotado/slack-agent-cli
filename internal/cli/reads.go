package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/abigotado/slack-agent-cli/internal/cursor"
	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

func newMeCommand(dependencies Dependencies) *cobra.Command {
	var name string
	command := &cobra.Command{Use: "me", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
		if err != nil {
			return err
		}
		identity, err := dependencies.Auth.AuthTest(command.Context(), current.token)
		if err != nil {
			return err
		}
		if !identityMatchesProfile(current.profile, identity) {
			return credentialIdentityChanged()
		}
		return dependencies.Output.Success(identity, &output.Meta{Profile: name, WorkspaceID: identity.WorkspaceID, ContentTrust: "untrusted"})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	return command
}

func newConversationsCommand(dependencies Dependencies) *cobra.Command {
	command := &cobra.Command{Use: "conversations", Args: exactArgs(0), RunE: subcommandRequired}
	command.AddCommand(newConversationsListCommand(dependencies), newConversationGetCommand(dependencies))
	return command
}

func newConversationsListCommand(dependencies Dependencies) *cobra.Command {
	var name, types, cursorValue string
	var limit int
	command := &cobra.Command{Use: "list", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := validateLimit(limit); err != nil {
			return err
		}
		normalizedTypes, err := normalizeTypes(types)
		if err != nil {
			return err
		}
		current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
		if err != nil {
			return err
		}
		query := fmt.Sprintf("types=%s&limit=%d", normalizedTypes, limit)
		profileIdentity := profile.Identity(current.profile)
		upstream, err := cursor.Unbind(cursorValue, profileIdentity, current.profile.WorkspaceID, "conversations.list", query)
		if err != nil {
			return usageError("INVALID_CURSOR", "cursor does not match this request", err)
		}
		page, err := dependencies.Conversations.ConversationsList(command.Context(), current.token, slack.ListOptions{Types: normalizedTypes, Limit: limit, Cursor: upstream})
		if err != nil {
			return err
		}
		next, err := cursor.Bind(profileIdentity, current.profile.WorkspaceID, "conversations.list", query, page.NextCursor)
		if err != nil {
			return err
		}
		page.NextCursor = ""
		count := len(page.Conversations)
		return dependencies.Output.Success(page.Conversations, &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID, ContentTrust: "untrusted", Count: &count, NextCursor: next})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().StringVar(&types, "types", "", "comma-separated public_channel,private_channel,im,mpim")
	command.Flags().IntVar(&limit, "limit", 0, "page size 1-100")
	command.Flags().StringVar(&cursorValue, "cursor", "", "bound cursor from prior response")
	return command
}

func newConversationGetCommand(dependencies Dependencies) *cobra.Command {
	var name string
	command := &cobra.Command{Use: "get CONVERSATION_ID", Args: exactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
		if err != nil {
			return err
		}
		conversation, _, err := preflightTarget(command.Context(), dependencies, current, args[0], policy.Read)
		if err != nil {
			return err
		}
		return dependencies.Output.Success(conversation, &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID, ContentTrust: "untrusted"})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	return command
}

func newMessagesCommand(dependencies Dependencies) *cobra.Command {
	command := &cobra.Command{Use: "messages", Args: exactArgs(0), RunE: subcommandRequired}
	command.AddCommand(newHistoryCommand(dependencies), newThreadCommand(dependencies))
	command.AddCommand(newSendCommand(dependencies))
	return command
}

func newHistoryCommand(dependencies Dependencies) *cobra.Command {
	var name, conversationID, cursorValue, oldest, latest string
	var limit int
	command := &cobra.Command{Use: "history", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := validateLimit(limit); err != nil {
			return err
		}
		if oldest != "" {
			if err := validateTimestamp(oldest, "--oldest"); err != nil {
				return err
			}
		}
		if latest != "" {
			if err := validateTimestamp(latest, "--latest"); err != nil {
				return err
			}
		}
		current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
		if err != nil {
			return err
		}
		if _, _, err := preflightTarget(command.Context(), dependencies, current, conversationID, policy.Read); err != nil {
			return err
		}
		query := fmt.Sprintf("conversation=%s&limit=%d&oldest=%s&latest=%s", conversationID, limit, oldest, latest)
		profileIdentity := profile.Identity(current.profile)
		upstream, err := cursor.Unbind(cursorValue, profileIdentity, current.profile.WorkspaceID, "conversations.history", query)
		if err != nil {
			return usageError("INVALID_CURSOR", "cursor does not match this request", err)
		}
		page, err := dependencies.Messages.History(command.Context(), current.token, slack.HistoryOptions{ConversationID: conversationID, Limit: limit, Cursor: upstream, Oldest: oldest, Latest: latest})
		if err != nil {
			return err
		}
		next, err := cursor.Bind(profileIdentity, current.profile.WorkspaceID, "conversations.history", query, page.NextCursor)
		if err != nil {
			return err
		}
		count := len(page.Messages)
		return dependencies.Output.Success(page.Messages, &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID, ContentTrust: "untrusted", Count: &count, NextCursor: next})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().StringVar(&conversationID, "conversation-id", "", "exact conversation ID")
	command.Flags().IntVar(&limit, "limit", 0, "page size 1-100")
	command.Flags().StringVar(&cursorValue, "cursor", "", "bound cursor from prior response")
	command.Flags().StringVar(&oldest, "oldest", "", "inclusive oldest Slack timestamp")
	command.Flags().StringVar(&latest, "latest", "", "inclusive latest Slack timestamp")
	return command
}

func newThreadCommand(dependencies Dependencies) *cobra.Command {
	var name, conversationID, threadTS, cursorValue string
	var limit int
	command := &cobra.Command{Use: "thread", Args: exactArgs(0), RunE: func(command *cobra.Command, _ []string) error {
		if err := validateLimit(limit); err != nil {
			return err
		}
		if err := validateTimestamp(threadTS, "--thread-ts"); err != nil {
			return err
		}
		current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
		if err != nil {
			return err
		}
		if _, _, err := preflightTarget(command.Context(), dependencies, current, conversationID, policy.Read); err != nil {
			return err
		}
		query := fmt.Sprintf("conversation=%s&thread=%s&limit=%d", conversationID, threadTS, limit)
		profileIdentity := profile.Identity(current.profile)
		upstream, err := cursor.Unbind(cursorValue, profileIdentity, current.profile.WorkspaceID, "conversations.replies", query)
		if err != nil {
			return usageError("INVALID_CURSOR", "cursor does not match this request", err)
		}
		page, err := dependencies.Messages.Replies(command.Context(), current.token, slack.ThreadOptions{ConversationID: conversationID, ThreadTS: threadTS, Limit: limit, Cursor: upstream})
		if err != nil {
			return err
		}
		next, err := cursor.Bind(profileIdentity, current.profile.WorkspaceID, "conversations.replies", query, page.NextCursor)
		if err != nil {
			return err
		}
		count := len(page.Messages)
		return dependencies.Output.Success(page.Messages, &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID, ContentTrust: "untrusted", Count: &count, NextCursor: next})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().StringVar(&conversationID, "conversation-id", "", "exact conversation ID")
	command.Flags().StringVar(&threadTS, "thread-ts", "", "exact parent Slack timestamp")
	command.Flags().IntVar(&limit, "limit", 0, "page size 1-100")
	command.Flags().StringVar(&cursorValue, "cursor", "", "bound cursor from prior response")
	return command
}

func newUsersCommand(dependencies Dependencies) *cobra.Command {
	command := &cobra.Command{Use: "users", Args: exactArgs(0), RunE: subcommandRequired}
	command.AddCommand(newUserGetCommand(dependencies))
	return command
}
func newUserGetCommand(dependencies Dependencies) *cobra.Command {
	var name string
	command := &cobra.Command{Use: "get USER_ID", Args: exactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if err := profile.ValidateSlackID(args[0]); err != nil {
			return usageError("INVALID_USER_ID", "user ID is invalid", err)
		}
		current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
		if err != nil {
			return err
		}
		user, err := dependencies.Users.UserInfo(command.Context(), current.token, args[0])
		if err != nil {
			return err
		}
		if user.ID != args[0] {
			return errx.New(errx.Internal, "IDENTITY_MISMATCH", "Slack returned a different user identity", "report this defect")
		}
		return dependencies.Output.Success(user, &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID, ContentTrust: "untrusted"})
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	return command
}
