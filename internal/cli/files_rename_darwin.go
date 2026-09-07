//go:build darwin

package cli

import "golang.org/x/sys/unix"

func renameDownloadNoReplace(directory int, source, destination string) error {
	return unix.RenameatxNp(directory, source, directory, destination, unix.RENAME_EXCL)
}
