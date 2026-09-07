package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"

	"github.com/abigotado/slack-agent-cli/internal/errx"
	"github.com/abigotado/slack-agent-cli/internal/slack"
)

// downloadDirectory is the local I/O boundary used to test failed publication and cleanup.
type downloadDirectory interface {
	Lstat(string) (os.FileInfo, error)
	OpenFile(string, int, os.FileMode) (*os.File, error)
	Remove(string) error
	Publish(string, string) error
}

type rootedDownloadDirectory struct{ *os.Root }

func (d rootedDownloadDirectory) Publish(source, destination string) error {
	directory, err := d.Open(".")
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }() // No pending writes on this directory handle.
	return renameDownloadNoReplace(int(directory.Fd()), source, destination)
}

func saveDownload(ctx context.Context, dependencies Dependencies, current session, file slack.FileDetails, destination string) (slack.DownloadResult, string, error) {
	path, err := filepath.Abs(destination)
	if err != nil {
		return slack.DownloadResult{}, "", usageError("INVALID_FILE_OUTPUT", "output path is invalid", err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return slack.DownloadResult{}, "", usageError("INVALID_FILE_OUTPUT", "output directory is unavailable", err)
	}
	defer func() { _ = root.Close() }() // Directory handle has no pending writes.
	result, err := writeDownload(ctx, dependencies, current, file, rootedDownloadDirectory{root}, filepath.Base(path))
	if err != nil {
		return result, "", err
	}
	return result, path, nil
}

func writeDownload(ctx context.Context, dependencies Dependencies, current session, file slack.FileDetails, directory downloadDirectory, name string) (result slack.DownloadResult, resultErr error) {
	if _, err := directory.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return result, errx.New(errx.Conflict, "FILE_OUTPUT_EXISTS", "output path already exists", "choose a new local output file")
		}
		return result, errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot inspect output path", "check the output directory permissions")
	}
	temporary := ".slack-download-" + rand.Text()
	target, err := directory.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return result, errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot create download file", "check the local output directory")
	}
	published := false
	defer func() {
		_ = target.Close() // Explicit close handles success; failure cleanup owns the partial file.
		if published {
			return
		} // Successful rename consumed the temporary name.
		if err := directory.Remove(temporary); err != nil {
			if resultErr == nil {
				resultErr = errx.New(errx.Internal, "FILE_CLEANUP_FAILED", "download temporary file cleanup failed", "inspect the output directory before retrying")
			} else {
				primary := *errx.As(resultErr)
				primary.Hint += "; temporary download cleanup also failed; inspect the output directory"
				resultErr = &primary
			}
		}
	}()
	result, err = dependencies.Files.DownloadFile(ctx, current.token, file, current.profile.WorkspaceID, target)
	if err != nil {
		return result, err
	}
	if err := target.Sync(); err != nil {
		return result, errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot sync download file", "check local storage")
	}
	if err := target.Close(); err != nil {
		return result, errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot close download file", "check local storage")
	}
	if err := directory.Publish(temporary, name); err != nil {
		if errors.Is(err, os.ErrExist) {
			return result, errx.New(errx.Conflict, "FILE_OUTPUT_EXISTS", "output path appeared during download", "inspect the output and choose a new local file")
		}
		return result, errx.New(errx.Internal, "FILE_OUTPUT_FAILED", "cannot atomically publish download file", "check directory permissions and filesystem support for no-overwrite rename")
	}
	published = true
	return result, nil
}
