// Package ffmpeg implements the continuous FFmpeg/XCB X11 capture stream:
// a fixed-argument ffmpeg child process scales frames server-side and writes
// raw RGB24 to stdout, while a drain goroutine keeps stderr from blocking.
package ffmpeg

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/rarnu/sshdesk-go/internal/capture"
)

const (
	stderrChunk      = 256
	stderrChunksKept = 8
	stderrDetailMax  = 2048
)

// Capture mirrors the Python FFmpegX11Capture.
type Capture struct {
	Executable      string
	DisplayName     string
	DesktopWidth    int
	DesktopHeight   int
	TargetWidth     int
	TargetHeight    int
	FramesPerSecond float64

	process      *exec.Cmd
	stdout       io.ReadCloser
	stderrPipe   io.ReadCloser
	buffer       []byte
	waitDone     chan struct{}
	stderrDone   chan struct{}
	stderrMu     sync.Mutex
	stderrChunks [][]byte
}

// New builds a capture; the process starts lazily on the first Capture call.
func New(executable, displayName string, desktopWidth, desktopHeight int) (*Capture, error) {
	info, err := os.Stat(executable)
	if err != nil || info.IsDir() {
		return nil, fmt.Errorf("ffmpeg executable is unavailable")
	}
	return &Capture{
		Executable:      executable,
		DisplayName:     displayName,
		DesktopWidth:    desktopWidth,
		DesktopHeight:   desktopHeight,
		TargetWidth:     desktopWidth,
		TargetHeight:    desktopHeight,
		FramesPerSecond: 60.0,
	}, nil
}

// SetTargetSize restarts the stream on the next Capture when the size moves.
func (c *Capture) SetTargetSize(width, height int) {
	if width != c.TargetWidth || height != c.TargetHeight {
		c.TargetWidth, c.TargetHeight = width, height
		c.stop()
	}
}

// SetFrameRate restarts the stream on the next Capture when the rate moves.
func (c *Capture) SetFrameRate(framesPerSecond float64) error {
	if framesPerSecond < 0.5 || framesPerSecond > 120.0 {
		return fmt.Errorf("capture FPS must be between 0.5 and 120")
	}
	if framesPerSecond != c.FramesPerSecond {
		c.FramesPerSecond = framesPerSecond
		c.stop()
	}
	return nil
}

// CommandLine is the exact child argument vector (never a shell).
func CommandLine(executable, displayName string, desktopWidth, desktopHeight, targetWidth, targetHeight int, framesPerSecond float64) []string {
	return []string{
		executable,
		"-nostdin",
		"-loglevel", "error",
		"-thread_queue_size", "1",
		"-f", "x11grab",
		"-draw_mouse", "0",
		"-framerate", formatG(framesPerSecond),
		"-video_size", fmt.Sprintf("%dx%d", desktopWidth, desktopHeight),
		"-i", displayName + "+0,0",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=bicubic", targetWidth, targetHeight),
		"-pix_fmt", "rgb24",
		"-fps_mode", "passthrough",
		"-f", "rawvideo",
		"pipe:1",
	}
}

// formatG mirrors Python's %g float formatting.
func formatG(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func (c *Capture) start() error {
	if c.process != nil {
		return nil
	}
	argv := CommandLine(
		c.Executable, c.DisplayName,
		c.DesktopWidth, c.DesktopHeight,
		c.TargetWidth, c.TargetHeight,
		c.FramesPerSecond,
	)
	command := exec.Command(argv[0], argv[1:]...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	c.process = command
	c.stdout = stdout
	c.stderrPipe = stderrPipe
	c.stderrMu.Lock()
	c.stderrChunks = nil
	c.stderrMu.Unlock()
	c.waitDone = make(chan struct{})
	go func() {
		command.Wait()
		close(c.waitDone)
	}()
	c.stderrDone = make(chan struct{})
	go c.drainStderr(stderrPipe, c.stderrDone)
	c.buffer = make([]byte, c.TargetWidth*c.TargetHeight*3)
	return nil
}

// drainStderr keeps the pipe empty so a chatty ffmpeg cannot block, and
// retains the tail for error reports.
func (c *Capture) drainStderr(stream io.Reader, done chan<- struct{}) {
	defer close(done)
	if stream == nil {
		return
	}
	chunk := make([]byte, stderrChunk)
	for {
		n, err := stream.Read(chunk)
		if n > 0 {
			kept := make([]byte, n)
			copy(kept, chunk[:n])
			c.stderrMu.Lock()
			c.stderrChunks = append(c.stderrChunks, kept)
			if len(c.stderrChunks) > stderrChunksKept {
				c.stderrChunks = c.stderrChunks[len(c.stderrChunks)-stderrChunksKept:]
			}
			c.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (c *Capture) stderrDetail() string {
	c.stderrMu.Lock()
	joined := bytes.Join(c.stderrChunks, nil)
	c.stderrMu.Unlock()
	if len(joined) > stderrDetailMax {
		joined = joined[:stderrDetailMax]
	}
	return string(bytes.TrimSpace(bytes.ToValidUTF8(joined, []byte("�"))))
}

// Frame is one captured RGB image with its timing and content digest.
type Frame struct {
	RGB           []byte // RGB24 pixels at target size
	CapturedNs    int64
	ContentDigest []byte
}

// Capture reads exactly one frame; an EOF or short read ends the stream.
func (c *Capture) Capture() (Frame, error) {
	if c.process == nil && c.stdout == nil {
		if err := c.start(); err != nil {
			return Frame{}, err
		}
	}
	if c.stdout == nil {
		return Frame{}, fmt.Errorf("FFmpeg X11 capture did not start")
	}
	if _, err := io.ReadFull(c.stdout, c.buffer); err != nil {
		c.stop()
		detail := c.stderrDetail()
		if detail != "" {
			return Frame{}, fmt.Errorf("FFmpeg X11 capture stream ended: %s", detail)
		}
		return Frame{}, fmt.Errorf("FFmpeg X11 capture stream ended")
	}
	rgb := make([]byte, len(c.buffer))
	copy(rgb, c.buffer)
	return Frame{
		RGB:           rgb,
		CapturedNs:    time.Now().UnixNano(),
		ContentDigest: capture.DigestPixels(rgb),
	}, nil
}

func (c *Capture) stop() {
	process := c.process
	stdout := c.stdout
	waitDone := c.waitDone
	stderrDone := c.stderrDone
	c.process = nil
	c.stdout = nil
	c.stderrPipe = nil
	c.waitDone = nil
	c.stderrDone = nil
	c.buffer = nil
	if process == nil {
		if stdout != nil {
			stdout.Close()
		}
		return
	}
	if stdout != nil {
		stdout.Close()
	}
	select {
	case <-waitDone:
	default:
		_ = process.Process.Signal(syscall.SIGTERM)
		select {
		case <-waitDone:
		case <-time.After(500 * time.Millisecond):
			_ = process.Process.Kill()
			<-waitDone
		}
	}
	if stderrDone != nil {
		select {
		case <-stderrDone:
		case <-time.After(time.Second):
		}
	}
}

// Close releases the child process and all streams.
func (c *Capture) Close() { c.stop() }
