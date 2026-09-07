package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
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
		var page slack.MessagePage
		if threadTS == "" {
			page, err = dependencies.Messages.History(command.Context(), current.token, slack.HistoryOptions{ConversationID: conversationID, Oldest: messageTS, Latest: messageTS, Inclusive: true, Limit: 1})
		} else {
			page, err = dependencies.Messages.Replies(command.Context(), current.token, slack.ThreadOptions{ConversationID: conversationID, ThreadTS: threadTS, Oldest: messageTS, Latest: messageTS, Inclusive: true, Limit: 1})
		}
		if err != nil {
			return err
		}
		attached := false
		if len(page.Messages) == 1 && page.Messages[0].TS == messageTS && (threadTS == "" || page.Messages[0].ThreadTS == threadTS || messageTS == threadTS) {
			for _, file := range page.Messages[0].Files {
				if file.ID == args[0] {
					attached = true
					break
				}
			}
		}
		if !attached {
			return errx.New(errx.NotFound, "FILE_NOT_IN_MESSAGE", "file is not attached to the exact message", "verify the file, conversation and message IDs; provide --thread-ts for a reply")
		}
		file, err := dependencies.Files.FileInfo(command.Context(), current.token, args[0])
		if err != nil {
			return err
		}
		if file.ID != args[0] {
			return errx.New(errx.Internal, "FILE_IDENTITY_MISMATCH", "Slack returned a different file identity", "re-read the exact attachment")
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

func saveDownload(ctx context.Context, dependencies Dependencies, current session, file slack.FileDetails, destination string) (result slack.DownloadResult, path string, resultErr error) {
	path, err := filepath.Abs(destination)
	if err != nil {
		return result, "", usageError("INVALID_FILE_OUTPUT", "output path is invalid", err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return result, "", usageError("INVALID_FILE_OUTPUT", "output directory is unavailable", err)
	}
	defer func() { _ = root.Close() }() // Directory handle has no pending writes.
	name := filepath.Base(path)
	if _, err := root.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		return result, "", errx.New(errx.Conflict, "FILE_OUTPUT_EXISTS", "output path exists or cannot be inspected", "choose a new local output file")
	}
	temporary := ".slack-download-" + rand.Text()
	target, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return result, "", errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot create download file", "check the local output directory")
	}
	defer func() {
		_ = target.Close() // Explicit close below handles success; on failure cleanup owns the partial file.
		if err := root.Remove(temporary); err != nil {
			resultErr = errx.New(errx.Conflict, "FILE_CLEANUP_FAILED", "download temporary file cleanup failed", "inspect the output directory before retrying")
		}
	}()
	result, err = dependencies.Files.DownloadFile(ctx, current.token, file, current.profile.WorkspaceID, target)
	if err != nil {
		return result, "", err
	}
	if err := target.Sync(); err != nil {
		return result, "", errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot sync download file", "check local storage")
	}
	if err := target.Close(); err != nil {
		return result, "", errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot close download file", "check local storage")
	}
	if err := root.Link(temporary, name); err != nil {
		return result, "", errx.New(errx.Conflict, "FILE_OUTPUT_NOT_PUBLISHED", "download could not be published without overwriting", "inspect the output path and choose a new file")
	}
	return result, path, nil
}
