//go:build windows

package session

import "time"

func waitWritable(fd int, timeout time.Duration) error {
	time.Sleep(timeout)
	return nil
}
