package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"

	"github.com/abigotado/slack-agent-cli/internal/contract"
)

// ErrTTYUnavailable marks the absence of a usable controlling terminal.
var ErrTTYUnavailable = errors.New("controlling terminal is unavailable")

var (
	ErrTTYIO          = errors.New("controlling terminal I/O failed")
	ErrTTYInterrupted = errors.New("terminal token input was interrupted")
	ErrTTYRestore     = errors.New("terminal echo restoration failed")
)

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
	var signals chan os.Signal
	if tracked := ttyInterruptSignals(); len(tracked) > 0 {
		signals = make(chan os.Signal, 1)
		signal.Notify(signals, tracked...)
		defer signal.Stop(signals)
	}
	return readTokenTTY(device, disableTTYEcho, signals)
}

type ttyReadResult struct {
	payload    []byte
	readErr    error
	newlineErr error
}

func readTokenTTY(device ttyDevice, disable echoDisabler, signals <-chan os.Signal) (token string, resultErr error) {
	restore, err := disable(int(device.Fd()))
	if err != nil {
		return "", fmt.Errorf("%w: disable terminal echo", ErrTTYUnavailable)
	}
	var restoreOnce, closeOnce sync.Once
	var restoreErr, closeErr error
	restoreTTY := func() error {
		restoreOnce.Do(func() { restoreErr = restore() })
		return restoreErr
	}
	closeTTY := func() error {
		closeOnce.Do(func() { closeErr = device.Close() })
		return closeErr
	}
	defer func() {
		if err := restoreTTY(); err != nil && !errors.Is(resultErr, ErrTTYRestore) {
			token = ""
			wrapped := fmt.Errorf("%w: %v", ErrTTYRestore, err)
			if resultErr != nil {
				resultErr = errors.Join(resultErr, wrapped)
			} else {
				resultErr = wrapped
			}
		}
		if err := closeTTY(); err != nil && !errors.Is(resultErr, ErrTTYIO) {
			token = ""
			wrapped := fmt.Errorf("%w: close controlling terminal: %v", ErrTTYIO, err)
			if resultErr != nil {
				resultErr = errors.Join(resultErr, wrapped)
			} else {
				resultErr = wrapped
			}
		}
	}()

	if _, err := io.WriteString(device, "Slack token: "); err != nil {
		return "", fmt.Errorf("%w: write terminal prompt: %v", ErrTTYIO, err)
	}
	readResults := make(chan ttyReadResult, 1)
	go func() {
		reader := bufio.NewReader(io.LimitReader(device, contract.MaxTokenBytes+3))
		payload, readErr := reader.ReadBytes('\n')
		_, newlineErr := io.WriteString(device, "\n")
		readResults <- ttyReadResult{payload: payload, readErr: readErr, newlineErr: newlineErr}
	}()

	var readResult ttyReadResult
	select {
	case received := <-signals:
		restoreErr := restoreTTY()
		closeErr := closeTTY()
		<-readResults
		return "", ttySignalError(received, restoreErr, closeErr)
	case readResult = <-readResults:
	}
	select {
	case received := <-signals:
		return "", ttySignalError(received, restoreTTY(), closeTTY())
	default:
	}
	if readResult.readErr != nil && !errors.Is(readResult.readErr, io.EOF) {
		return "", fmt.Errorf("%w: read terminal token: %v", ErrTTYIO, readResult.readErr)
	}
	if errors.Is(readResult.readErr, io.EOF) && len(readResult.payload) == 0 {
		return "", fmt.Errorf("%w: terminal closed before token input", ErrTTYInterrupted)
	}
	if readResult.newlineErr != nil {
		return "", fmt.Errorf("%w: write terminal newline: %v", ErrTTYIO, readResult.newlineErr)
	}
	return validateTokenPayload(readResult.payload)
}

func ttySignalError(received os.Signal, restoreErr, closeErr error) error {
	result := fmt.Errorf("%w: %s", ErrTTYInterrupted, received)
	if restoreErr != nil {
		result = errors.Join(result, fmt.Errorf("%w: %v", ErrTTYRestore, restoreErr))
	}
	if closeErr != nil {
		result = errors.Join(result, fmt.Errorf("%w: close controlling terminal: %v", ErrTTYIO, closeErr))
	}
	return result
}
