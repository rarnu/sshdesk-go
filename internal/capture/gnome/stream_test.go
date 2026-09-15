package gnome

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
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
	s := &streamCapture{}
	s.stdout = io.NopCloser(bytes.NewReader(nil))
	s.buffer = make([]byte, 10)
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
	s := &streamCapture{}
	s.stdout = io.NopCloser(bytes.NewReader(pixels))
	s.buffer = make([]byte, len(pixels))

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
