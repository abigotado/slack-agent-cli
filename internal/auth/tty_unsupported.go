//go:build !darwin && !linux

package auth

import "os"

func ttyInterruptSignals() []os.Signal { return nil }

func disableTTYEcho(int) (func() error, error) {
	return nil, ErrTTYUnavailable
}
