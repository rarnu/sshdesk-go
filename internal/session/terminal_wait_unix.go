//go:build !windows

package session

import (
	"time"

	"golang.org/x/sys/unix"
)

func waitWritable(fd int, timeout time.Duration) error {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	for {
		_, err := unix.Poll(fds, int(timeout.Milliseconds()))
		if err == unix.EINTR {
			continue
		}
		return err
	}
}
