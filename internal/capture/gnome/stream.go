package gnome

import (
	"bytes"
	"fmt"
	"io"
	"os/exec"
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

// CommandLine is the fixed gst-launch argument vector (never a shell),
// replacing the Python appsink pipeline with a continuous fdsink stream.
func CommandLine(executable string, node uint32, width, height int) []string {
	return []string{
		executable, "-q",
		"pipewiresrc", fmt.Sprintf("path=%d", node), "do-timestamp=true", "keepalive-time=100",
		"!",
		"queue", "leaky=downstream", "max-size-buffers=1",
		"!",
		"videoconvert", "n-threads=2",
		"!",
		"videoscale", "method=1", "n-threads=2",
		"!",
		fmt.Sprintf("video/x-raw,format=RGB,width=%d,height=%d,pixel-aspect-ratio=1/1", width, height),
		"!",
		"fdsink", "fd=1",
	}
}

// streamFrame is one captured RGB24 frame at stream (target) size.
type streamFrame struct {
	RGB           []byte
	CapturedNs    int64
	ContentDigest []byte
}

// frameStream is the seam the capture uses for its video stream.
type frameStream interface {
	Capture() (streamFrame, error)
	Close()
}

// streamCapture mirrors the ffmpeg process management: a lazily started
// gst-launch child writes scaled RGB24 frames to stdout while a drain
// goroutine keeps stderr from blocking.
type streamCapture struct {
	executable   string
	node         uint32
	targetWidth  int
	targetHeight int

	process      *exec.Cmd
	stdout       io.ReadCloser
	stderrPipe   io.ReadCloser
	buffer       []byte
	waitDone     chan struct{}
	stderrDone   chan struct{}
	stderrMu     sync.Mutex
	stderrChunks [][]byte
}

func newStreamCapture(executable string, node uint32, width, height int) *streamCapture {
	return &streamCapture{
		executable:   executable,
		node:         node,
		targetWidth:  width,
		targetHeight: height,
	}
}

func (s *streamCapture) start() error {
	if s.process != nil {
		return nil
	}
	argv := CommandLine(s.executable, s.node, s.targetWidth, s.targetHeight)
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
		return fmt.Errorf("could not create the GNOME capture pipeline: %v", err)
	}
	s.process = command
	s.stdout = stdout
	s.stderrPipe = stderrPipe
	s.stderrMu.Lock()
	s.stderrChunks = nil
	s.stderrMu.Unlock()
	waitDone := make(chan struct{})
	s.waitDone = waitDone
	go func() {
		command.Wait()
		close(waitDone)
	}()
	s.stderrDone = make(chan struct{})
	go s.drainStderr(stderrPipe, s.stderrDone)
	s.buffer = make([]byte, s.targetWidth*s.targetHeight*3)
	return nil
}

// drainStderr keeps the pipe empty and retains the tail for error reports.
func (s *streamCapture) drainStderr(stream io.Reader, done chan<- struct{}) {
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
			s.stderrMu.Lock()
			s.stderrChunks = append(s.stderrChunks, kept)
			if len(s.stderrChunks) > stderrChunksKept {
				s.stderrChunks = s.stderrChunks[len(s.stderrChunks)-stderrChunksKept:]
			}
			s.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *streamCapture) stderrDetail() string {
	s.stderrMu.Lock()
	joined := bytes.Join(s.stderrChunks, nil)
	s.stderrMu.Unlock()
	if len(joined) > stderrDetailMax {
		joined = joined[:stderrDetailMax]
	}
	return string(bytes.TrimSpace(bytes.ToValidUTF8(joined, []byte("�"))))
}

// Capture reads exactly one frame; an EOF or short read ends the stream.
func (s *streamCapture) Capture() (streamFrame, error) {
	if s.process == nil && s.stdout == nil {
		if err := s.start(); err != nil {
			return streamFrame{}, err
		}
	}
	if s.stdout == nil {
		return streamFrame{}, fmt.Errorf("the pipeline stopped")
	}
	if _, err := io.ReadFull(s.stdout, s.buffer); err != nil {
		s.stop()
		if detail := s.stderrDetail(); detail != "" {
			return streamFrame{}, fmt.Errorf("stream ended: %s", detail)
		}
		return streamFrame{}, fmt.Errorf("stream ended")
	}
	rgb := make([]byte, len(s.buffer))
	copy(rgb, s.buffer)
	return streamFrame{
		RGB:           rgb,
		CapturedNs:    time.Now().UnixNano(),
		ContentDigest: capture.DigestPixels(rgb),
	}, nil
}

func (s *streamCapture) stop() {
	process := s.process
	stdout := s.stdout
	waitDone := s.waitDone
	stderrDone := s.stderrDone
	s.process = nil
	s.stdout = nil
	s.stderrPipe = nil
	s.waitDone = nil
	s.stderrDone = nil
	s.buffer = nil
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
func (s *streamCapture) Close() { s.stop() }
