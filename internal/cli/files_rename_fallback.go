//go:build !darwin && !linux

package cli

import "errors"

func renameDownloadNoReplace(_ int, _, _ string) error {
	return errors.ErrUnsupported // Never fall back to a check followed by overwriting rename.
}
