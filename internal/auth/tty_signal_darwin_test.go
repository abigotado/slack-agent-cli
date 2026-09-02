//go:build darwin

package auth

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestReadTokenTTYRestoresRealPTYAfterInterrupt(t *testing.T) {
	if _, err := os.Stat("/usr/bin/script"); err != nil {
		t.Skip("script utility is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/usr/bin/script", "-q", "/dev/null", os.Args[0], "-test.run=^TestTTYSignalHelper$", "-test.v")
	command.Env = append(os.Environ(), "SLACK_AGENT_CLI_TTY_SIGNAL_HELPER=1")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	prefix, err := reader.ReadString(':')
	if err != nil || !strings.Contains(prefix, "Slack token:") {
		t.Fatalf("prompt=%q err=%v stderr=%q", prefix, err, stderr.String())
	}
	children, err := exec.Command("/usr/bin/pgrep", "-P", strconv.Itoa(command.Process.Pid)).Output()
	if err != nil {
		t.Fatalf("find helper process: %v", err)
	}
	fields := strings.Fields(string(children))
	if len(fields) != 1 {
		t.Fatalf("helper process count=%d pids=%q", len(fields), string(children))
	}
	helperPID, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(helperPID, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	remainder, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait: %v output=%q stderr=%q", err, string(remainder), stderr.String())
	}
	output := prefix + string(remainder)
	if !strings.Contains(output, "TTY_HELPER_ECHO_ON") {
		t.Fatalf("terminal echo was not confirmed: %q stderr=%q", output, stderr.String())
	}
}

func TestTTYSignalHelper(t *testing.T) {
	if os.Getenv("SLACK_AGENT_CLI_TTY_SIGNAL_HELPER") != "1" {
		return
	}
	if _, err := ReadTokenTTY(); !errors.Is(err, ErrTTYInterrupted) {
		t.Fatalf("ReadTokenTTY error=%v", err)
	}
	device, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := device.Close(); err != nil {
			t.Errorf("close terminal: %v", err)
		}
	}()
	state, err := unix.IoctlGetTermios(int(device.Fd()), unix.TIOCGETA)
	if err != nil {
		t.Fatal(err)
	}
	if state.Lflag&unix.ECHO == 0 {
		t.Fatal("terminal echo remains disabled")
	}
	fmt.Println("TTY_HELPER_ECHO_ON")
}
