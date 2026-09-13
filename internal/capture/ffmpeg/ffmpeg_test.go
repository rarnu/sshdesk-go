package ffmpeg

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestCommandLineMatchesSpec pins the exact child argument vector.
func TestCommandLineMatchesSpec(t *testing.T) {
	got := CommandLine("ffmpeg", ":0", 1920, 1080, 640, 360, 60.0)
	want := []string{
		"ffmpeg",
		"-nostdin",
		"-loglevel", "error",
		"-thread_queue_size", "1",
		"-f", "x11grab",
		"-draw_mouse", "0",
		"-framerate", "60",
		"-video_size", "1920x1080",
		"-i", ":0+0,0",
		"-vf", "scale=640:360:flags=bicubic",
		"-pix_fmt", "rgb24",
		"-fps_mode", "passthrough",
		"-f", "rawvideo",
		"pipe:1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CommandLine() = %q, want %q", got, want)
	}
}

func TestCommandLineFormatsFractionalFPS(t *testing.T) {
	got := CommandLine("ffmpeg", ":0", 100, 100, 100, 100, 29.97)
	if got[11] != "29.97" {
		t.Fatalf("framerate argument = %q, want %q", got[11], "29.97")
	}
}

func TestStderrDrainKeepsTail(t *testing.T) {
	c := &Capture{}
	payload := append(bytes.Repeat([]byte("x"), 4096), []byte("frame dropped")...)
	done := make(chan struct{})
	c.drainStderr(bytes.NewReader(payload), done)
	<-done
	detail := c.stderrDetail()
	if !strings.HasSuffix(detail, "frame dropped") {
		t.Fatalf("stderr detail %q does not end with the tail", detail)
	}
	if len(detail) > stderrDetailMax {
		t.Fatalf("stderr detail is %d bytes, want at most %d", len(detail), stderrDetailMax)
	}
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("closed") }

func TestStderrDrainIgnoresReadErrors(t *testing.T) {
	c := &Capture{}
	done := make(chan struct{})
	c.drainStderr(brokenReader{}, done)
	<-done
	if detail := c.stderrDetail(); detail != "" {
		t.Fatalf("stderr detail = %q, want empty", detail)
	}
	nilDone := make(chan struct{})
	c.drainStderr(nil, nilDone)
	<-nilDone
}

func TestCaptureReportsDrainedStderrAfterStreamEnds(t *testing.T) {
	c := &Capture{}
	c.stdout = io.NopCloser(bytes.NewReader(nil))
	c.buffer = make([]byte, 10)
	c.stderrChunks = [][]byte{[]byte("capturer error")}

	_, err := c.Capture()
	if err == nil || !strings.Contains(err.Error(), "stream ended: capturer error") {
		t.Fatalf("Capture() error = %v, want the drained stderr detail", err)
	}
	if c.process != nil || c.stdout != nil {
		t.Fatalf("Capture() left process=%v stdout=%v behind", c.process, c.stdout)
	}
}

func TestCaptureStreamEndedWithoutDetail(t *testing.T) {
	c := &Capture{}
	c.stdout = io.NopCloser(bytes.NewReader(nil))
	c.buffer = make([]byte, 10)

	_, err := c.Capture()
	if err == nil || err.Error() != "FFmpeg X11 capture stream ended" {
		t.Fatalf("Capture() error = %v, want the bare stream-ended message", err)
	}
}

func TestCaptureReadsOneFrame(t *testing.T) {
	pixels := bytes.Repeat([]byte{1, 2, 3}, 4)
	c := &Capture{}
	c.stdout = io.NopCloser(bytes.NewReader(pixels))
	c.buffer = make([]byte, len(pixels))

	frame, err := c.Capture()
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !bytes.Equal(frame.RGB, pixels) {
		t.Fatalf("Capture() pixels = %v, want %v", frame.RGB, pixels)
	}
	if len(frame.ContentDigest) != 8 {
		t.Fatalf("content digest is %d bytes, want 8", len(frame.ContentDigest))
	}
	if frame.CapturedNs == 0 {
		t.Fatal("Capture() did not stamp the frame")
	}
}

func TestSetFrameRateBoundsAreEnforced(t *testing.T) {
	c := &Capture{FramesPerSecond: 60.0, TargetWidth: 64, TargetHeight: 64}
	if err := c.SetFrameRate(30.0); err != nil {
		t.Fatalf("SetFrameRate(30) error = %v", err)
	}
	if c.FramesPerSecond != 30.0 {
		t.Fatalf("FramesPerSecond = %v, want 30", c.FramesPerSecond)
	}
	if err := c.SetFrameRate(500.0); err == nil || !strings.Contains(err.Error(), "between 0.5 and 120") {
		t.Fatalf("SetFrameRate(500) error = %v, want the bounds message", err)
	}
	if err := c.SetFrameRate(0.1); err == nil {
		t.Fatal("SetFrameRate(0.1) did not fail")
	}
}

func TestNewRejectsMissingExecutable(t *testing.T) {
	if _, err := New("/nonexistent/ffmpeg", ":0", 100, 100); err == nil {
		t.Fatal("New() did not fail for a missing executable")
	}
}
