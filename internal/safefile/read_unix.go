//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

// Package safefile provides bounded, no-follow local file reads.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// ReadRegular opens path without following its final symlink, validates the
// descriptor, and never allocates beyond maximum+1 bytes.
func ReadRegular(path string, maximum int64) ([]byte, os.FileInfo, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, nil, errors.New("invalid file descriptor")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("path is not a regular file")
	}
	if info.Size() < 0 || info.Size() > maximum {
		return nil, info, fmt.Errorf("file exceeds %d-byte bound", maximum)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, info, err
	}
	if int64(len(payload)) > maximum {
		return nil, info, fmt.Errorf("file exceeds %d-byte bound", maximum)
	}
	return payload, info, nil
}
