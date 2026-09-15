package gnome

import (
	"bytes"
	"errors"
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

	// streamBufferCount keeps three frame buffers in circulation: one the
	// drain goroutine reads into, one published as the latest frame, and
	// one handed out to the caller of Capture.
	streamBufferCount = 3
)

// frameWaitTimeout bounds how long Capture waits for a fresh frame from an
// idle pipeline, matching the Python appsink try-pull-sample timeout. It is
// a package variable so tests can shrink it.
var frameWaitTimeout = 2 * time.Second

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
// gst-launch child writes scaled RGB24 frames to stdout. A drain goroutine
// reads the pipe continuously so the child never blocks on a full pipe and
// publishes only the newest frame, like the appsink max-buffers=1 drop=true
// queue it replaces; Capture waits for a frame it has not handed out yet.
//
// Buffer lifetime: frame buffers cycle through freeBuf -> drain -> latest
// -> outstanding (owned by the Capture caller) -> freeBuf. A returned frame
// stays valid until the caller's next Capture.
type streamCapture struct {
	executable   string
	node         uint32
	targetWidth  int
	targetHeight int

	mu          sync.Mutex
	process     *exec.Cmd
	stdout      io.ReadCloser
	stderrPipe  io.ReadCloser
	waitDone    chan struct{}
	stderrDone  chan struct{}
	drainStop   chan struct{}
	drainDone   chan struct{}
	frameCh     chan struct{}
	freeBuf     chan []byte
	draining    bool
	stopped     bool
	latest      streamFrame
	seq         uint64
	consumed    uint64
	outstanding []byte
	drainErr    error

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

// ensureChannels lazily creates the coordination channels and seeds the
// buffer pool; tests attach pipes to a literal streamCapture that never saw
// newStreamCapture. Call with s.mu held.
func (s *streamCapture) ensureChannels() {
	if s.frameCh != nil {
		return
	}
	s.frameCh = make(chan struct{}, 1)
	s.drainStop = make(chan struct{})
	s.drainDone = make(chan struct{})
	s.freeBuf = make(chan []byte, streamBufferCount)
	if size := s.targetWidth * s.targetHeight * 3; size > 0 {
		for i := 0; i < streamBufferCount; i++ {
			s.freeBuf <- make([]byte, size)
		}
	}
}

// signal wakes a waiting Capture without blocking the drain.
func signalFrame(frameCh chan<- struct{}) {
	select {
	case frameCh <- struct{}{}:
	default:
	}
}

func (s *streamCapture) start() error {
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
	waitDone := make(chan struct{})
	stderrDone := make(chan struct{})
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		stdout.Close()
		_ = command.Process.Kill()
		_ = command.Wait()
		return errors.New("the pipeline stopped")
	}
	s.ensureChannels()
	s.process = command
	s.stdout = stdout
	s.stderrPipe = stderrPipe
	s.waitDone = waitDone
	s.stderrDone = stderrDone
	s.draining = true
	s.stderrMu.Lock()
	s.stderrChunks = nil
	s.stderrMu.Unlock()
	s.mu.Unlock()
	go func() {
		command.Wait()
		close(waitDone)
	}()
	go s.drainStderr(stderrPipe, stderrDone)
	go s.drainFrames(stdout)
	return nil
}

// ensureStarted spawns the child on first use and launches the drain
// goroutine for pre-attached test pipes.
func (s *streamCapture) ensureStarted() error {
	s.mu.Lock()
	if s.stopped {
		drainErr := s.drainErr
		s.mu.Unlock()
		if drainErr != nil {
			return drainErr
		}
		return errors.New("the pipeline stopped")
	}
	s.ensureChannels()
	if s.process != nil {
		s.mu.Unlock()
		return nil
	}
	if s.stdout != nil {
		// Test seam: a pre-attached pipe without a child process.
		if !s.draining {
			s.draining = true
			stdout := s.stdout
			s.mu.Unlock()
			go s.drainFrames(stdout)
			return nil
		}
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()
	return s.start()
}

// drainFrames reads frames back to back and publishes the newest one. An
// EOF or short read ends the stream; the pipe closing underneath a blocked
// read is how stop releases the goroutine.
func (s *streamCapture) drainFrames(stdout io.Reader) {
	frameCh := s.frameCh
	freeBuf := s.freeBuf
	drainStop := s.drainStop
	defer close(s.drainDone)
	for {
		var buf []byte
		select {
		case buf = <-freeBuf:
		case <-drainStop:
			return
		}
		if _, err := io.ReadFull(stdout, buf); err != nil {
			s.mu.Lock()
			if s.drainErr == nil {
				if detail := s.stderrDetail(); detail != "" {
					s.drainErr = fmt.Errorf("stream ended: %s", detail)
				} else {
					s.drainErr = errors.New("stream ended")
				}
			}
			s.mu.Unlock()
			signalFrame(frameCh)
			return
		}
		s.mu.Lock()
		old := s.latest
		s.latest = streamFrame{RGB: buf, CapturedNs: time.Now().UnixNano()}
		s.seq++
		s.mu.Unlock()
		if old.RGB != nil {
			freeBuf <- old.RGB
		}
		signalFrame(frameCh)
	}
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

// Capture waits for the next frame the caller has not seen yet, like
// try-pull-sample against a drop=true appsink queue. The returned buffer is
// recycled by the next Capture call. A stream that produces nothing for
// frameWaitTimeout reports the Python appsink timeout error.
func (s *streamCapture) Capture() (streamFrame, error) {
	if err := s.ensureStarted(); err != nil {
		return streamFrame{}, err
	}
	timer := time.NewTimer(frameWaitTimeout)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if s.seq > s.consumed {
			frame := s.latest
			frame.ContentDigest = capture.DigestPixels(frame.RGB)
			s.latest = streamFrame{}
			s.consumed = s.seq
			if s.outstanding != nil {
				s.freeBuf <- s.outstanding
			}
			s.outstanding = frame.RGB
			s.mu.Unlock()
			return frame, nil
		}
		drainErr := s.drainErr
		s.mu.Unlock()
		if drainErr != nil {
			s.stop()
			return streamFrame{}, drainErr
		}
		s.mu.Lock()
		frameCh := s.frameCh
		stopped := s.stopped
		s.mu.Unlock()
		if stopped {
			return streamFrame{}, errors.New("the pipeline stopped")
		}
		select {
		case <-frameCh:
		case <-timer.C:
			return streamFrame{}, errors.New("no frame arrived within 2 seconds")
		}
	}
}

func (s *streamCapture) stop() {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	s.stopped = true
	if s.drainErr == nil {
		s.drainErr = errors.New("stream ended")
	}
	if s.drainStop != nil {
		close(s.drainStop)
	}
	process := s.process
	stdout := s.stdout
	waitDone := s.waitDone
	stderrDone := s.stderrDone
	draining := s.draining
	drainDone := s.drainDone
	frameCh := s.frameCh
	s.process = nil
	s.stdout = nil
	s.stderrPipe = nil
	s.waitDone = nil
	s.stderrDone = nil
	s.mu.Unlock()
	if frameCh != nil {
		signalFrame(frameCh)
	}
	if stdout != nil {
		stdout.Close()
	}
	if draining && drainDone != nil {
		select {
		case <-drainDone:
		case <-time.After(time.Second):
		}
	}
	if process == nil {
		return
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
