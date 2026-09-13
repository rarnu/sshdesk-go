//go:build darwin || linux

package session

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/rylena/sshdesk-go/internal/render"
	"github.com/rylena/sshdesk-go/internal/render/ansi"
)

func testCapabilities() render.Capabilities {
	return render.Capabilities{
		Term:     "test",
		Color:    render.Color256,
		Mouse:    true,
		SGRMouse: true,
		Unicode:  true,
	}
}

func TestTerminalRestoredAfterException(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("PTY unavailable in this environment: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	fd := int(tty.Fd())
	before, err := terminalAttributes(fd)
	if err != nil {
		t.Fatal(err)
	}
	writer := ansi.NewWriter(testCapabilities(), "SSHDESK")
	state := NewTerminalState(fd, fd, writer)
	if err := state.Enter(); err != nil {
		t.Fatal(err)
	}
	// A panic mid-session must still restore the terminal.
	if err := state.Restore(); err != nil {
		t.Fatal(err)
	}
	after, err := terminalAttributes(fd)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("termios not restored:\nbefore %+v\nafter  %+v", before, after)
	}
}

func buildServerBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "sshdesk")
	build := exec.Command("go", "build", "-o", binary, "github.com/rylena/sshdesk-go/cmd/sshdesk")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	return binary
}

// ptyReader drains a pty master in non-blocking mode; pty masters do not
// support os.File read deadlines, so EAGAIN stands in for a short timeout.
type ptyReader struct {
	master *os.File
	chunk  []byte
}

func newPTYReader(t *testing.T, master *os.File) *ptyReader {
	t.Helper()
	if err := syscall.SetNonblock(int(master.Fd()), true); err != nil {
		t.Fatal(err)
	}
	return &ptyReader{master: master, chunk: make([]byte, 65536)}
}

func (r *ptyReader) waitFor(output *bytes.Buffer, deadline time.Time, condition func() bool) {
	for time.Now().Before(deadline) && !condition() {
		n, err := r.master.Read(r.chunk)
		if n > 0 {
			output.Write(r.chunk[:n])
		}
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			// EIO: the session closed the slave side.
			return
		}
		if n == 0 {
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestPlainPTYSessionDetachesAndRestores(t *testing.T) {
	binary := buildServerBinary(t)
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("PTY unavailable in this environment: %v", err)
	}
	defer ptmx.Close()
	defer tty.Close()
	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}
	before, err := terminalAttributes(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}

	stderr := &bytes.Buffer{}
	command := exec.Command(binary, "server",
		"--capture", "synthetic", "--synthetic-static", "--no-input")
	command.Env = append(os.Environ(), "TERM=xterm-256color", "SSHDESK_COLOR=256")
	command.Stdin = tty
	command.Stdout = tty
	command.Stderr = stderr
	// A new session without a controlling terminal, matching the Python
	// harness (start_new_session=True). A controlling terminal makes the
	// macOS kernel hang the child in exit state.
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	reader := newPTYReader(t, ptmx)

	output := &bytes.Buffer{}
	reader.waitFor(output, time.Now().Add(5*time.Second), func() bool {
		return bytes.Contains(output.Bytes(), []byte("\x1b[?1049h"))
	})
	if !bytes.Contains(output.Bytes(), []byte("\x1b[?1049h")) {
		t.Fatalf("session never entered the alternate screen:\nstderr: %s", stderr)
	}

	if err := pty.Setsize(ptmx, &pty.Winsize{Rows: 30, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	reader.waitFor(output, time.Now().Add(5*time.Second), func() bool {
		return bytes.Count(output.Bytes(), []byte("\x1b[2J")) >= 2
	})
	if got := bytes.Count(output.Bytes(), []byte("\x1b[2J")); got < 2 {
		t.Fatalf("resize did not trigger a canvas reset (%d clears)", got)
	}

	if _, err := ptmx.Write([]byte("\x1d\x1d")); err != nil {
		t.Fatal(err)
	}
	// The exit flag must not be the channel itself: a polling condition that
	// receives from the channel would consume the value and leave the final
	// assertion waiting on nothing.
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
	if !bytes.Contains(output.Bytes(), []byte("\x1b[?1049l")) {
		t.Error("session never left the alternate screen")
	}
	after, err := terminalAttributes(int(tty.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Errorf("termios not restored:\nbefore %+v\nafter  %+v", before, after)
	}
}

func TestServerRejectsNonPTYSessionCleanly(t *testing.T) {
	binary := buildServerBinary(t)
	command := exec.Command(binary, "server",
		"--capture", "synthetic", "--synthetic-static", "--no-input")
	command.Env = append(os.Environ(), "TERM=xterm-256color")
	command.Stdin = bytes.NewReader(nil)
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	err := command.Run()
	if err == nil {
		t.Fatal("non-PTY session must fail")
	}
	exitError, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("unexpected error: %v", err)
	}
	if exitError.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1 (stderr: %s)", exitError.ExitCode(), stderr)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("interactive terminal")) {
		t.Errorf("stderr = %q, want interactive terminal message", stderr)
	}
}
