package wayland

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSelectBackendMatrix(t *testing.T) {
	installed := func(names ...string) func(string) bool {
		return func(name string) bool {
			for _, candidate := range names {
				if candidate == name {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		desktop   string
		available func(string) bool
		want      string
	}{
		{"GNOME", installed("gnome-screenshot", "grim"), "gnome-screenshot"},
		{"ubuntu:GNOME", installed("gnome-screenshot"), "gnome-screenshot"},
		{"Unity", installed("gnome-screenshot"), "gnome-screenshot"},
		{"GNOME", installed("grim"), "grim"},
		{"KDE", installed("spectacle", "grim"), "spectacle"},
		{"plasma", installed("spectacle"), "spectacle"},
		{"KDE", installed("grim"), "grim"},
		{"sway", installed("grim", "gnome-screenshot"), "grim"},
		{"sway", installed("gnome-screenshot"), "gnome-screenshot"},
		{"", installed("spectacle"), "spectacle"},
		{"GNOME", installed("spectacle"), "spectacle"},
	}
	for index, tc := range cases {
		got, err := SelectBackend(tc.desktop, tc.available)
		if err != nil {
			t.Fatalf("case %d: SelectBackend(%q) error = %v", index, tc.desktop, err)
		}
		if got != tc.want {
			t.Fatalf("case %d: SelectBackend(%q) = %q, want %q", index, tc.desktop, got, tc.want)
		}
	}
}

func TestSelectBackendMissingHelpers(t *testing.T) {
	_, err := SelectBackend("sway", func(string) bool { return false })
	want := "Wayland capture needs grim (wlroots), gnome-screenshot (GNOME), or spectacle (KDE Plasma)"
	if err == nil || err.Error() != want {
		t.Fatalf("SelectBackend() error = %v, want %q", err, want)
	}
}

func TestCommandVectors(t *testing.T) {
	if got := Command("grim", ""); !reflect.DeepEqual(got, []string{"grim", "-c", "-t", "png", "-"}) {
		t.Fatalf("grim vector = %q", got)
	}
	if got := Command("gnome-screenshot", "/tmp/frame.png"); !reflect.DeepEqual(got, []string{"gnome-screenshot", "-f", "/tmp/frame.png"}) {
		t.Fatalf("gnome-screenshot vector = %q", got)
	}
	if got := Command("spectacle", "/tmp/frame.png"); !reflect.DeepEqual(got, []string{"spectacle", "-b", "-n", "-o", "/tmp/frame.png"}) {
		t.Fatalf("spectacle vector = %q", got)
	}
}

func TestExecRunnerTimeoutBecomesCleanError(t *testing.T) {
	_, err := execRunner([]string{"sleep", "10"}, 50*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "sleep Wayland capture timed out after 0.05 seconds") {
		t.Fatalf("execRunner() error = %v, want the timeout message", err)
	}
}

func TestExecRunnerCapturesExitCode(t *testing.T) {
	result, err := execRunner([]string{"false"}, time.Second)
	if err != nil {
		t.Fatalf("execRunner() error = %v", err)
	}
	if result.exitCode != 1 {
		t.Fatalf("exit code = %d, want 1", result.exitCode)
	}
}

func encodePNG(t *testing.T, img *image.RGBA) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	return buffer.Bytes()
}

func TestGrimFailureReportsStderr(t *testing.T) {
	c := &Capture{Backend: "grim", run: func(argv []string, timeout time.Duration) (runResult, error) {
		return runResult{exitCode: 1, stderr: []byte("no output\n")}, nil
	}}
	_, err := c.Capture()
	if err == nil || err.Error() != "grim Wayland capture failed: no output" {
		t.Fatalf("Capture() error = %v", err)
	}

	c.run = func(argv []string, timeout time.Duration) (runResult, error) {
		return runResult{exitCode: 1}, nil
	}
	_, err = c.Capture()
	if err == nil || err.Error() != "grim Wayland capture failed: unknown error" {
		t.Fatalf("Capture() error = %v, want the unknown-error fallback", err)
	}
}

func TestGrimCaptureScalesAndFingerprints(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 4, 2))
	payload := encodePNG(t, source)
	var argv []string
	c := &Capture{Backend: "grim", run: func(args []string, timeout time.Duration) (runResult, error) {
		argv = append([]string{}, args...)
		return runResult{stdout: payload}, nil
	}}
	if err := c.SetTargetSize(2, 1); err != nil {
		t.Fatalf("SetTargetSize() error = %v", err)
	}
	frame, err := c.Capture()
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if !reflect.DeepEqual(argv, []string{"grim", "-c", "-t", "png", "-"}) {
		t.Fatalf("helper argv = %q", argv)
	}
	if frame.Image.Rect.Dx() != 2 || frame.Image.Rect.Dy() != 1 {
		t.Fatalf("frame size = %dx%d, want 2x1", frame.Image.Rect.Dx(), frame.Image.Rect.Dy())
	}
	if frame.Width() != 4 || frame.Height() != 2 {
		t.Fatalf("desktop size = %dx%d, want 4x2", frame.Width(), frame.Height())
	}
	if len(frame.ContentDigest) != 8 {
		t.Fatalf("content digest is %d bytes, want 8", len(frame.ContentDigest))
	}
	if width, height := c.Size(); width != 4 || height != 2 {
		t.Fatalf("Size() = (%d, %d), want (4, 2)", width, height)
	}
}

func TestFileHelperCaptureRemovesTempFile(t *testing.T) {
	payload := encodePNG(t, image.NewRGBA(image.Rect(0, 0, 3, 3)))
	var argv []string
	c := &Capture{Backend: "gnome-screenshot", run: func(args []string, timeout time.Duration) (runResult, error) {
		argv = append([]string{}, args...)
		if err := os.WriteFile(args[2], payload, 0o600); err != nil {
			t.Fatalf("writing fake screenshot: %v", err)
		}
		return runResult{}, nil
	}}
	frame, err := c.Capture()
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if frame.Image.Rect.Dx() != 3 || frame.Image.Rect.Dy() != 3 {
		t.Fatalf("frame size = %dx%d, want 3x3", frame.Image.Rect.Dx(), frame.Image.Rect.Dy())
	}
	if len(argv) != 3 || argv[0] != "gnome-screenshot" || argv[1] != "-f" {
		t.Fatalf("helper argv = %q", argv)
	}
	if _, err := os.Stat(argv[2]); !os.IsNotExist(err) {
		t.Fatalf("temporary screenshot %q was not removed", argv[2])
	}
}

func TestNewRequiresWaylandDisplay(t *testing.T) {
	t.Setenv("WAYLAND_DISPLAY", "")
	if _, err := New(); err == nil || err.Error() != "WAYLAND_DISPLAY is not set; Wayland capture is unavailable" {
		t.Fatalf("New() error = %v", err)
	}
}

func TestSetTargetSizeBounds(t *testing.T) {
	c := &Capture{}
	if err := c.SetTargetSize(0, 10); err == nil {
		t.Fatal("SetTargetSize(0, 10) did not fail")
	}
	if err := c.SetTargetSize(16385, 10); err == nil {
		t.Fatal("SetTargetSize(16385, 10) did not fail")
	}
}

func TestCursorPositionUnavailable(t *testing.T) {
	c := &Capture{}
	if x, y, ok := c.CursorPosition(); ok || x != 0 || y != 0 {
		t.Fatalf("CursorPosition() = (%d, %d, %v), want (0, 0, false)", x, y, ok)
	}
}
