package session

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/term"
)

// TerminalState manages the raw-terminal and alternate-screen lifecycle with
// guaranteed restoration.
type TerminalState struct {
	InputFd  int
	OutputFd int

	writer terminalWriter
	active bool
	saved  *term.State
}

type terminalWriter interface {
	Enter() []byte
	Leave() []byte
}

// NewTerminalState binds a terminal state to stdin/stdout by default.
func NewTerminalState(inputFd, outputFd int, writer terminalWriter) *TerminalState {
	if inputFd < 0 {
		inputFd = int(os.Stdin.Fd())
	}
	if outputFd < 0 {
		outputFd = int(os.Stdout.Fd())
	}
	return &TerminalState{InputFd: inputFd, OutputFd: outputFd, writer: writer}
}

// Size returns the terminal dimensions, falling back to 80x24.
func Size(fd int) (int, int) {
	width, height, err := term.GetSize(fd)
	if err != nil {
		width, height = 80, 24
	}
	return max(1, width), max(1, height)
}

// WriteAll writes every byte, retrying short writes and blocking conditions.
func WriteAll(fd int, data []byte) error {
	for len(data) > 0 {
		written, err := writeFd(fd, data)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				if err := waitWritable(fd, 100*time.Millisecond); err != nil {
					return err
				}
				continue
			}
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return err
		}
		data = data[written:]
	}
	return nil
}

// Enter switches to raw mode and emits the writer's enter sequence.
func (t *TerminalState) Enter() error {
	if !term.IsTerminal(t.InputFd) || !term.IsTerminal(t.OutputFd) {
		return errors.New("SSHDESK client requires an interactive terminal")
	}
	if err := t.enterRaw(); err != nil {
		return err
	}
	t.active = true
	if err := WriteAll(t.OutputFd, t.writer.Enter()); err != nil {
		_ = t.Restore()
		return err
	}
	return nil
}

// Restore emits the leave sequence and restores the saved terminal modes.
func (t *TerminalState) Restore() error {
	if !t.active {
		return nil
	}
	var writeErr error
	if err := WriteAll(t.OutputFd, t.writer.Leave()); err != nil {
		writeErr = err
	}
	t.restoreRaw()
	t.active = false
	return writeErr
}

// Active reports whether Enter succeeded.
func (t *TerminalState) Active() bool { return t.active }
