package cli

import (
	"context"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/output"
	"github.com/abigotado/slack-agent-cli/internal/policy"
	"github.com/abigotado/slack-agent-cli/internal/profile"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

func newFilesCommand(dependencies Dependencies) *cobra.Command {
	command := &cobra.Command{Use: "files", Args: exactArgs(0), RunE: subcommandRequired}
	command.AddCommand(newFileCommand(dependencies, false), newFileCommand(dependencies, true))
	return command
}

func newFileCommand(dependencies Dependencies, download bool) *cobra.Command {
	var name, conversationID, messageTS, threadTS, destination string
	use := "get FILE_ID"
	if download {
		use = "download FILE_ID"
	}
	command := &cobra.Command{Use: use, Args: exactArgs(1), RunE: func(command *cobra.Command, args []string) error {
		if err := requireProfile(name); err != nil {
			return err
		}
		if profile.ValidateSlackID(args[0]) != nil || !strings.HasPrefix(args[0], "F") {
			return usageError("INVALID_FILE_ID", "file ID is invalid", nil)
		}
		if err := validateTimestamp(messageTS, "--message-ts"); err != nil {
			return err
		}
		if threadTS != "" {
			if err := validateTimestamp(threadTS, "--thread-ts"); err != nil {
				return err
			}
		}
		if download && (destination == "" || destination == "-" || strings.HasSuffix(destination, string(os.PathSeparator))) {
			return usageError("FILE_OUTPUT_REQUIRED", "download requires an explicit local output file", nil)
		}
		current, err := loadSession(command.Context(), dependencies, name, profile.CapabilityRead)
		if err != nil {
			return err
		}
		if _, _, err := preflightTarget(command.Context(), dependencies, current, conversationID, policy.Read); err != nil {
			return err
		}
		message, err := readFileMessage(command.Context(), dependencies, current, conversationID, messageTS, threadTS)
		if err != nil {
			return err
		}
		attached := false
		for _, file := range message.Files {
			if file.ID == args[0] {
				attached = true
				break
			}
		}
		if !attached {
			return errx.New(errx.NotFound, "FILE_NOT_IN_MESSAGE", "file is not attached to the exact message", "verify the file, conversation and message IDs; provide --thread-ts for a reply")
		}
		file, err := dependencies.Files.FileInfo(command.Context(), current.token, args[0])
		if err != nil {
			return err
		}
		meta := &output.Meta{Profile: name, WorkspaceID: current.profile.WorkspaceID, ContentTrust: "untrusted"}
		if !download {
			return dependencies.Output.Success(file.File, meta)
		}
		result, path, err := saveDownload(command.Context(), dependencies, current, file, destination)
		if err != nil {
			return err
		}
		return dependencies.Output.Success(struct {
			File slack.File `json:"file"`
			Path string     `json:"path"`
			slack.DownloadResult
		}{File: file.File, Path: path, DownloadResult: result}, meta)
	}}
	command.Flags().StringVar(&name, "profile", "", "exact profile name")
	command.Flags().StringVar(&conversationID, "conversation-id", "", "allowlisted conversation ID")
	command.Flags().StringVar(&messageTS, "message-ts", "", "exact message containing the file")
	command.Flags().StringVar(&threadTS, "thread-ts", "", "parent timestamp when the attachment is in a reply")
	if download {
		command.Flags().StringVar(&destination, "output", "", "new local file path; never overwritten")
	}
	return command
}

// readFileMessage permits a leading parent without treating it as attachment proof.
// The exact timestamp window and fixed page budget prevent an unbounded thread scan.
func readFileMessage(ctx context.Context, dependencies Dependencies, current session, conversationID, messageTS, threadTS string) (slack.Message, error) {
	notFound := errx.New(errx.NotFound, "FILE_NOT_IN_MESSAGE", "file message was not found exactly", "verify the conversation and message IDs; provide --thread-ts for a reply")
	if threadTS == "" {
		page, err := dependencies.Messages.History(ctx, current.token, slack.HistoryOptions{ConversationID: conversationID, Oldest: messageTS, Latest: messageTS, Inclusive: true, Limit: 1})
		if err != nil {
			return slack.Message{}, err
		}
		if len(page.Messages) != 1 || page.Messages[0].TS != messageTS {
			return slack.Message{}, notFound
		}
		return page.Messages[0], nil
	}
	cursor := ""
	for pageNumber := 0; pageNumber < 2; pageNumber++ {
		page, err := dependencies.Messages.Replies(ctx, current.token, slack.ThreadOptions{ConversationID: conversationID, ThreadTS: threadTS, Oldest: messageTS, Latest: messageTS, Inclusive: true, Limit: 2, Cursor: cursor})
		if err != nil {
			return slack.Message{}, err
		}
		if len(page.Messages) > 2 {
			return slack.Message{}, notFound
		}
		var match *slack.Message
		for i := range page.Messages {
			message := &page.Messages[i]
			if message.TS == messageTS {
				if match != nil || (message.ThreadTS != threadTS && !(messageTS == threadTS && message.ThreadTS == "")) {
					return slack.Message{}, notFound
				}
				match = message
			} else if message.TS != threadTS || (message.ThreadTS != "" && message.ThreadTS != threadTS) {
				return slack.Message{}, notFound
			}
		}
		if match != nil {
			return *match, nil
		}
		if page.NextCursor == "" || page.NextCursor == cursor {
			break
		}
		cursor = page.NextCursor
	}
	return slack.Message{}, notFound
}
