//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

// Package lockfile provides bounded advisory file locks.
package lockfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

const retryInterval = 25 * time.Millisecond

// With obtains an exclusive lock, runs fn, then releases it.
func With(ctx context.Context, path string, fn func() error) error {
	fd, err := unix.Open(path, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd) // Best effort after os.NewFile rejected the descriptor.
		return errors.New("open lock: invalid file descriptor")
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close() // Preserve the primary inspection failure.
		return fmt.Errorf("inspect lock: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		_ = file.Close() // No useful work occurred; report the policy failure.
		return errors.New("lock file is insecure")
	}

	for {
		err = unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			_ = file.Close() // Preserve the lock failure.
			return fmt.Errorf("acquire lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close() // Preserve cancellation as the recovery signal.
			return fmt.Errorf("acquire lock: %w", ctx.Err())
		case <-time.After(retryInterval):
		}
	}
	result := fn()
	if unlockErr := unix.Flock(int(file.Fd()), unix.LOCK_UN); unlockErr != nil {
		result = errors.Join(result, fmt.Errorf("release lock: %w", unlockErr))
	}
	if closeErr := file.Close(); closeErr != nil {
		result = errors.Join(result, fmt.Errorf("close lock: %w", closeErr))
	}
	return result
}
