//go:build linux

package cli

import "golang.org/x/sys/unix"

func renameDownloadNoReplace(directory int, source, destination string) error {
	return unix.Renameat2(directory, source, directory, destination, unix.RENAME_NOREPLACE)
}
