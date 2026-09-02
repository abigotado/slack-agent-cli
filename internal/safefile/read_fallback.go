//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

// Package safefile provides bounded local file reads on unsupported platforms.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func ReadRegular(path string, maximum int64) ([]byte, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
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
