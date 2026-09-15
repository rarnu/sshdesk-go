package gnome

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCommandLineMatchesSpec(t *testing.T) {
	got := CommandLine("gst-launch-1.0", 42, 1280, 720)
	want := []string{
		"gst-launch-1.0", "-q",
		"pipewiresrc", "path=42", "do-timestamp=true", "keepalive-time=100",
		"!",
		"queue", "leaky=downstream", "max-size-buffers=1",
		"!",
		"videoconvert", "n-threads=2",
		"!",
		"videoscale", "method=1", "n-threads=2",
		"!",
		"video/x-raw,format=RGB,width=1280,height=720,pixel-aspect-ratio=1/1",
		"!",
		"fdsink", "fd=1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CommandLine() = %q, want %q", got, want)
	}
}

func TestStreamCaptureReportsStderrAfterStreamEnds(t *testing.T) {
	s := &streamCapture{
		stdout:       io.NopCloser(bytes.NewReader(nil)),
		targetWidth:  5,
		targetHeight: 2,
	}
	s.stderrChunks = [][]byte{[]byte("capturer error")}

	_, err := s.Capture()
	if err == nil || err.Error() != "stream ended: capturer error" {
		t.Fatalf("Capture() error = %v, want the drained stderr detail", err)
	}
	if s.process != nil || s.stdout != nil {
		t.Fatalf("Capture() left process=%v stdout=%v behind", s.process, s.stdout)
	}
}

func TestStreamCaptureReadsOneFrame(t *testing.T) {
	pixels := bytes.Repeat([]byte{7, 8, 9}, 6)
	s := &streamCapture{
		stdout:       io.NopCloser(bytes.NewReader(pixels)),
		targetWidth:  6,
		targetHeight: 1,
	}

	frame, err := s.Capture()
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !bytes.Equal(frame.RGB, pixels) {
		t.Fatalf("Capture() pixels = %v, want %v", frame.RGB, pixels)
	}
	if len(frame.ContentDigest) != 8 || frame.CapturedNs == 0 {
		t.Fatal("Capture() did not digest and stamp the frame")
	}
	if _, err := s.Capture(); err == nil || err.Error() != "stream ended" {
		t.Fatalf("second Capture() error = %v, want the stream end", err)
	}
}

func TestStreamCaptureReturnsLatestFrame(t *testing.T) {
	stale := bytes.Repeat([]byte{1, 2, 3}, 8)
	fresh := bytes.Repeat([]byte{4, 5, 6}, 8)
	payload := append(bytes.Clone(stale), fresh...)
	s := &streamCapture{
		stdout:       io.NopCloser(bytes.NewReader(payload)),
		targetWidth:  8,
		targetHeight: 1,
	}
	if err := s.ensureStarted(); err != nil {
		t.Fatalf("ensureStarted() error = %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		seq := s.seq
		s.mu.Unlock()
		if seq >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the drain goroutine did not consume both frames")
		}
		time.Sleep(time.Millisecond)
	}

	frame, err := s.Capture()
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !bytes.Equal(frame.RGB, fresh) {
		t.Fatalf("Capture() pixels = %v, want the latest frame %v", frame.RGB, fresh)
	}
}

func TestStreamCaptureTimesOutWithoutFrames(t *testing.T) {
	restore := frameWaitTimeout
	frameWaitTimeout = 50 * time.Millisecond
	t.Cleanup(func() { frameWaitTimeout = restore })

	reader, writer := io.Pipe()
	defer writer.Close()
	s := &streamCapture{
		stdout:       reader,
		targetWidth:  4,
		targetHeight: 2,
	}
	_, err := s.Capture()
	if err == nil || err.Error() != "no frame arrived within 2 seconds" {
		t.Fatalf("Capture() error = %v, want the appsink timeout", err)
	}
	s.Close()
}

func TestStreamDrainKeepsTail(t *testing.T) {
	s := &streamCapture{}
	payload := append(bytes.Repeat([]byte("x"), 4096), []byte("frame dropped")...)
	done := make(chan struct{})
	s.drainStderr(bytes.NewReader(payload), done)
	<-done
	detail := s.stderrDetail()
	if !strings.HasSuffix(detail, "frame dropped") || len(detail) > stderrDetailMax {
		t.Fatalf("stderr detail %q (%d bytes)", detail, len(detail))
	}
}
