//go:build !windows

package session

import (
	"syscall"

	"golang.org/x/term"
)

func (t *TerminalState) enterRaw() error {
	state, err := term.MakeRaw(t.InputFd)
	if err != nil {
		return err
	}
	t.saved = state
	return nil
}

func (t *TerminalState) restoreRaw() {
	if t.saved != nil {
		_ = term.Restore(t.InputFd, t.saved)
		t.saved = nil
	}
}

func writeFd(fd int, data []byte) (int, error) {
	return syscall.Write(fd, data)
}
