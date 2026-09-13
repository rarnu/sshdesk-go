//go:build darwin

package session

import "golang.org/x/sys/unix"

func terminalAttributes(fd int) (*unix.Termios, error) {
	attributes, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return nil, err
	}
	// Darwin may set PENDIN after queued input is consumed. It is kernel
	// state, not a raw-mode setting owned by SSHDESK.
	masked := *attributes
	masked.Lflag &^= unix.PENDIN
	return &masked, nil
}
