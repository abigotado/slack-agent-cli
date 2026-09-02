//go:build darwin

package auth

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func ttyInterruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGTSTP}
}

func disableTTYEcho(fd int) (func() error, error) {
	original, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, err
	}
	hidden := *original
	hidden.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &hidden); err != nil {
		return nil, err
	}
	return func() error {
		return unix.IoctlSetTermios(fd, unix.TIOCSETA, original)
	}, nil
}
