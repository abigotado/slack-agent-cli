package auth

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTTY struct {
	reader io.Reader
	output bytes.Buffer
}

func (t *fakeTTY) Read(payload []byte) (int, error)  { return t.reader.Read(payload) }
func (t *fakeTTY) Write(payload []byte) (int, error) { return t.output.Write(payload) }
func (*fakeTTY) Fd() uintptr                         { return 7 }
func (*fakeTTY) Close() error                        { return nil }

func TestReadTokenTTYRestoresEchoAndDoesNotEchoSecret(t *testing.T) {
	t.Parallel()
	device := &fakeTTY{reader: strings.NewReader("credential-sentinel\n")}
	restored := false
	token, err := readTokenTTY(device, func(fd int) (func() error, error) {
		if fd != 7 {
			t.Fatalf("fd=%d", fd)
		}
		return func() error { restored = true; return nil }, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "credential-sentinel" || !restored {
		t.Fatalf("token=%q restored=%v", token, restored)
	}
	if strings.Contains(device.output.String(), token) {
		t.Fatal("secret was echoed")
	}
}

func TestReadTokenTTYRestoresEchoAfterInputFailure(t *testing.T) {
	t.Parallel()
	device := &fakeTTY{reader: errorReader{}}
	restored := false
	if _, err := readTokenTTY(device, func(int) (func() error, error) {
		return func() error { restored = true; return nil }, nil
	}, nil); err == nil {
		t.Fatal("input failure was accepted")
	}
	if !restored {
		t.Fatal("terminal echo was not restored")
	}
}

func TestReadTokenTTYRestoreFailureClearsToken(t *testing.T) {
	t.Parallel()
	device := &fakeTTY{reader: strings.NewReader("credential-sentinel\n")}
	token, err := readTokenTTY(device, func(int) (func() error, error) {
		return func() error { return errors.New("restore failed") }, nil
	}, nil)
	if err == nil || token != "" {
		t.Fatalf("token=%q err=%v", token, err)
	}
}

func TestReadTokenTTYMapsEchoFailureToUnavailable(t *testing.T) {
	t.Parallel()
	device := &fakeTTY{reader: strings.NewReader("credential-sentinel\n")}
	_, err := readTokenTTY(device, func(int) (func() error, error) {
		return nil, errors.New("not a terminal")
	}, nil)
	if !errors.Is(err, ErrTTYUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

func TestReadTokenTTYRestoresEchoOnInterrupt(t *testing.T) {
	t.Parallel()
	device := newBlockingTTY()
	signals := make(chan os.Signal, 1)
	restored := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := readTokenTTY(device, func(int) (func() error, error) {
			return func() error { close(restored); return nil }, nil
		}, signals)
		result <- err
	}()
	select {
	case <-device.reading:
	case <-time.After(time.Second):
		t.Fatal("TTY read did not start")
	}
	signals <- os.Interrupt
	select {
	case err := <-result:
		if !errors.Is(err, ErrTTYInterrupted) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TTY read did not stop after interrupt")
	}
	select {
	case <-restored:
	default:
		t.Fatal("terminal echo was not restored before returning")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

type blockingTTY struct {
	reading   chan struct{}
	closed    chan struct{}
	readOnce  sync.Once
	closeOnce sync.Once
}

func newBlockingTTY() *blockingTTY {
	return &blockingTTY{reading: make(chan struct{}), closed: make(chan struct{})}
}

func (t *blockingTTY) Read([]byte) (int, error) {
	t.readOnce.Do(func() { close(t.reading) })
	<-t.closed
	return 0, errors.New("terminal closed")
}
func (*blockingTTY) Write(payload []byte) (int, error) { return len(payload), nil }
func (*blockingTTY) Fd() uintptr                       { return 8 }
func (t *blockingTTY) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
	return nil
}
