//go:build darwin || linux

package session

import (
	"bytes"
	"os"
	"os/exec"
	"reflect"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestKittyProbeSelectsRealPixelSession mirrors the Python lifecycle test:
// the server probes the terminal, the test answers as a Kitty terminal, and
// the session must switch to pixel rendering with pixel mouse reporting.
func TestKittyProbeSelectsRealPixelSession(t *testing.T) {
	binary := buildServerBinary(t)
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("PTY unavailable in this environment: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 12, Cols: 40}); err != nil {
		t.Fatal(err)
	}
	before, err := terminalAttributes(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	stderr := &bytes.Buffer{}
	command := exec.Command(binary, "server",
		"--capture", "synthetic", "--synthetic-static", "--no-input")
	command.Env = append(os.Environ(),
		"TERM=xterm-kitty", "SSHDESK_RENDER=auto", "SSHDESK_COLOR=truecolor")
	command.Stdin = tty
	command.Stdout = tty
	command.Stderr = stderr
	// A new session without a controlling terminal, matching the Python
	// harness (start_new_session=True).
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	reader := newPTYReader(t, ptmx)

	output := &bytes.Buffer{}
	reader.waitFor(output, time.Now().Add(5*time.Second), func() bool {
		return bytes.Contains(output.Bytes(), []byte("a=q,t=d,f=24"))
	})
	if !bytes.Contains(output.Bytes(), []byte("a=q,t=d,f=24")) {
		t.Fatalf("session never sent the Kitty graphics query:\nstderr: %s", stderr)
	}
	// Answer as a Kitty terminal: graphics OK, 320x192 text area, 8x16 cells,
	// pixel mouse + synchronized output, then DA1 to close the probe.
	if _, err := ptmx.Write([]byte(
		"\x1b_Gi=1893;OK\x1b\\" +
			"\x1b[4;192;320t\x1b[6;16;8t" +
			"\x1b[?1016;2$y\x1b[?2026;2$y\x1b[?62;c")); err != nil {
		t.Fatal(err)
	}
	reader.waitFor(output, time.Now().Add(5*time.Second), func() bool {
		return bytes.Contains(output.Bytes(), []byte("\x1b_Ga=T"))
	})
	if !bytes.Contains(output.Bytes(), []byte("\x1b_Ga=T")) {
		t.Fatalf("session never placed a Kitty image:\nstderr: %s", stderr)
	}
	if !bytes.Contains(output.Bytes(), []byte("f=100")) {
		t.Error("Kitty placements must use PNG (f=100)")
	}
	if !bytes.Contains(output.Bytes(), []byte("\x1b[?1016h")) {
		t.Error("session must enable SGR pixel mouse reporting")
	}

	if _, err := ptmx.Write([]byte("\x1d\x1d")); err != nil {
		t.Fatal(err)
	}
	var exited atomic.Bool
	waitErr := make(chan error, 1)
	go func() {
		err := command.Wait()
		exited.Store(true)
		waitErr <- err
	}()
	reader.waitFor(output, time.Now().Add(5*time.Second), exited.Load)
	if !exited.Load() {
		_ = command.Process.Kill()
		t.Fatalf("session did not detach on double Ctrl+]\nstderr: %s", stderr)
	}
	if err := <-waitErr; err != nil {
		t.Fatalf("session exit: %v\nstderr: %s", err, stderr)
	}
	reader.waitFor(output, time.Now().Add(500*time.Millisecond), func() bool { return false })
	if !bytes.Contains(output.Bytes(), []byte("\x1b_Ga=d,d=A,q=1")) {
		t.Error("session never deleted all terminal-side images")
	}
	if !bytes.Contains(output.Bytes(), []byte("\x1b[?1016l")) {
		t.Error("session never disabled SGR pixel mouse reporting")
	}
	after, err := terminalAttributes(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("termios not restored:\nbefore %+v\nafter  %+v", before, after)
	}
}

// TestKittyModeFailsWithoutGraphics mirrors the Python error path:
// SSHDESK_RENDER=kitty against a terminal that does not answer the graphics
// query must fail with the documented message.
func TestKittyModeFailsWithoutGraphics(t *testing.T) {
	binary := buildServerBinary(t)
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("PTY unavailable in this environment: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()

	stderr := &bytes.Buffer{}
	command := exec.Command(binary, "server",
		"--capture", "synthetic", "--synthetic-static", "--no-input")
	command.Env = append(os.Environ(), "TERM=xterm-256color", "SSHDESK_RENDER=kitty")
	command.Stdin = tty
	command.Stdout = tty
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// Drain the master so the child's probe write cannot block.
	reader := newPTYReader(t, ptmx)
	output := &bytes.Buffer{}
	var exited atomic.Bool
	waitErr := make(chan error, 1)
	go func() {
		err := command.Wait()
		exited.Store(true)
		waitErr <- err
	}()
	reader.waitFor(output, time.Now().Add(10*time.Second), exited.Load)
	if !exited.Load() {
		_ = command.Process.Kill()
		t.Fatal("SSHDESK_RENDER=kitty without graphics must exit on its own")
	}
	err = <-waitErr
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected exit: %v (stderr: %s)", err, stderr)
	}
	if exitError.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1 (stderr: %s)", exitError.ExitCode(), stderr)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("does not support Kitty graphics")) {
		t.Errorf("stderr = %q, want the Kitty support message", stderr)
	}
}
