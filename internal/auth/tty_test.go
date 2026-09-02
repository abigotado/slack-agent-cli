package auth

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
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
	device := &fakeTTY{reader: strings.NewReader("xoxb-secret\n")}
	restored := false
	token, err := readTokenTTY(device, func(fd int) (func() error, error) {
		if fd != 7 {
			t.Fatalf("fd=%d", fd)
		}
		return func() error { restored = true; return nil }, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if token != "xoxb-secret" || !restored {
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
	}); err == nil {
		t.Fatal("input failure was accepted")
	}
	if !restored {
		t.Fatal("terminal echo was not restored")
	}
}

func TestReadTokenTTYRestoreFailureClearsToken(t *testing.T) {
	t.Parallel()
	device := &fakeTTY{reader: strings.NewReader("xoxb-secret\n")}
	token, err := readTokenTTY(device, func(int) (func() error, error) {
		return func() error { return errors.New("restore failed") }, nil
	})
	if err == nil || token != "" {
		t.Fatalf("token=%q err=%v", token, err)
	}
}

func TestReadTokenTTYMapsEchoFailureToUnavailable(t *testing.T) {
	t.Parallel()
	device := &fakeTTY{reader: strings.NewReader("xoxb-secret\n")}
	_, err := readTokenTTY(device, func(int) (func() error, error) {
		return nil, errors.New("not a terminal")
	})
	if !errors.Is(err, ErrTTYUnavailable) {
		t.Fatalf("err=%v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
