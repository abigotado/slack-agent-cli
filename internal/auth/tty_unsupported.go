//go:build !darwin && !linux

package auth

func disableTTYEcho(int) (func() error, error) {
	return nil, ErrTTYUnavailable
}
