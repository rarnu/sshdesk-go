//go:build !windows

package session

import (
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// readTerminalInput waits up to timeout for terminal input; timedOut reports
// that the deadline expired without data.
func readTerminalInput(fd int, buffer []byte, timeout time.Duration) (int, bool, error) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(timeout.Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, false, err
		}
		if n == 0 {
			return 0, true, nil
		}
		break
	}
	for {
		n, err := syscall.Read(fd, buffer)
		if err == syscall.EINTR {
			continue
		}
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			return 0, true, nil
		}
		return n, false, err
	}
}
