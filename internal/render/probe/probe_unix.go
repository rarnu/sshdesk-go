//go:build !windows

package probe

import (
	"os"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// IoctlPixelSize returns the terminal pixel size from TIOCGWINSZ as
// (width, height), or (0, 0) when the ioctl is unavailable.
func IoctlPixelSize(fd int) (int, int) {
	winsize, err := unix.IoctlGetWinsize(fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0
	}
	return int(winsize.Xpixel), int(winsize.Ypixel)
}

// ProbeGraphics queries the terminal through the SSH PTY before the input
// thread starts. It temporarily switches the input fd to raw mode and always
// restores the previous attributes.
func ProbeGraphics(inputFd, outputFd, columns, rows int, timeout float64) GraphicsProbe {
	if !term.IsTerminal(inputFd) || !term.IsTerminal(outputFd) {
		return GraphicsProbe{}
	}
	graphicsQuery := []byte("\x1b_Gi=" + strconv.Itoa(ProbeImageID) + ",s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\")
	if os.Getenv("TMUX") != "" {
		graphicsQuery = TmuxPassthrough(graphicsQuery)
	}
	payload := append(graphicsQuery, []byte("\x1b[14t\x1b[16t\x1b[?1016$p\x1b[?2026$p\x1b[c")...)

	var reply []byte
	saved, err := term.MakeRaw(inputFd)
	if err == nil {
		defer func() { _ = term.Restore(inputFd, saved) }()
		writeAllFd(outputFd, payload)
		deadline := time.Now().Add(secondsDuration(max(0.05, min(timeout, 2.0))))
		chunk := make([]byte, 4096)
		for len(reply) < 16384 {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				break
			}
			if !waitReadable(inputFd, remaining) {
				break
			}
			limit := min(4096, 16384-len(reply))
			n, readErr := syscall.Read(inputFd, chunk[:limit])
			if readErr != nil {
				if readErr == syscall.EINTR {
					continue
				}
				break
			}
			if n == 0 {
				break
			}
			reply = append(reply, chunk[:n]...)
			if da1RE.Match(reply) {
				break
			}
		}
	}
	pixelWidth, pixelHeight := IoctlPixelSize(outputFd)
	return ParseGraphicsProbe(reply, columns, rows, [2]int{pixelWidth, pixelHeight})
}

func writeAllFd(fd int, data []byte) {
	for len(data) > 0 {
		written, err := syscall.Write(fd, data)
		if err != nil {
			if err == syscall.EINTR {
				continue
			}
			return
		}
		data = data[written:]
	}
}

func waitReadable(fd int, timeout time.Duration) bool {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, max(1, int(timeout.Milliseconds())))
		if err == unix.EINTR {
			continue
		}
		return err == nil && n > 0
	}
}

func secondsDuration(value float64) time.Duration {
	return time.Duration(value * float64(time.Second))
}
