//go:build windows

package session

import (
	"syscall"
	"time"
)

// readTerminalInput reads console input. Windows console reads block, so the
// timeout is not honored; ESC disambiguation completes on the next byte.
func readTerminalInput(fd int, buffer []byte, timeout time.Duration) (int, bool, error) {
	n, err := syscall.Read(syscall.Handle(fd), buffer)
	return n, false, err
}
