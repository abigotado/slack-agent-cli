package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/abigotado/slack-agent-cli/internal/contract"
)

// ErrTTYUnavailable marks the absence of a usable controlling terminal.
var ErrTTYUnavailable = errors.New("controlling terminal is unavailable")

type ttyDevice interface {
	io.Reader
	io.Writer
	Fd() uintptr
	Close() error
}

type echoDisabler func(int) (func() error, error)

// ReadTokenTTY reads one bounded secret line directly from the controlling
// terminal while echo is disabled. The token never crosses stdin, argv, or
// the environment.
func ReadTokenTTY() (token string, resultErr error) {
	device, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("%w: open /dev/tty", ErrTTYUnavailable)
	}
	defer func() {
		if err := device.Close(); err != nil && resultErr == nil {
			token = ""
			resultErr = fmt.Errorf("close controlling terminal: %w", err)
		}
	}()
	return readTokenTTY(device, disableTTYEcho)
}

func readTokenTTY(device ttyDevice, disable echoDisabler) (token string, resultErr error) {
	restore, err := disable(int(device.Fd()))
	if err != nil {
		return "", fmt.Errorf("%w: disable terminal echo", ErrTTYUnavailable)
	}
	defer func() {
		if err := restore(); err != nil {
			token = ""
			restoreErr := fmt.Errorf("restore terminal echo: %w", err)
			if resultErr != nil {
				resultErr = errors.Join(resultErr, restoreErr)
			} else {
				resultErr = restoreErr
			}
		}
	}()

	if _, err := io.WriteString(device, "Slack token: "); err != nil {
		return "", fmt.Errorf("write terminal prompt: %w", err)
	}
	reader := bufio.NewReader(io.LimitReader(device, contract.MaxTokenBytes+2))
	payload, readErr := reader.ReadBytes('\n')
	_, newlineErr := io.WriteString(device, "\n")
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return "", fmt.Errorf("read terminal token: %w", readErr)
	}
	if newlineErr != nil {
		return "", fmt.Errorf("write terminal newline: %w", newlineErr)
	}
	if len(payload) > contract.MaxTokenBytes+1 {
		return "", errors.New("token input is empty or exceeds 8 KiB")
	}
	return validateTokenPayload(payload)
}
