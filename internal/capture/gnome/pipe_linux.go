//go:build linux

package gnome

import (
	"os"
	"syscall"
)

// growPipe raises the stdout pipe capacity toward the frame size so the
// gst-launch child and the drain goroutine trade frames with fewer blocking
// pipe syscalls. Unprivileged requests are capped by the kernel's
// pipe-max-size (1MiB by default); failures keep the default capacity.
func growPipe(pipe *os.File, size int) {
	const (
		fSetPipeSz = 1031
		pipeMax    = 1 << 20
	)
	if size > pipeMax {
		size = pipeMax
	}
	_, _, _ = syscall.Syscall(syscall.SYS_FCNTL, pipe.Fd(), fSetPipeSz, uintptr(size))
}
