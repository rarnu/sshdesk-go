package native

import (
	"image"
	"testing"
)

func TestBgraToRGBAChannelOrderAndStride(t *testing.T) {
	// Two pixels per row with a padded stride: BGRA in, RGBA out.
	src := []byte{
		10, 20, 30, 40, 50, 60, 70, 80, 0xEE, 0xEE, 0xEE, 0xEE,
		90, 100, 110, 120, 130, 140, 150, 160, 0xEE, 0xEE, 0xEE, 0xEE,
	}
	img := bgraToRGBA(src, 2, 2, 12)
	first := img.Pix[0:4]
	if first[0] != 30 || first[1] != 20 || first[2] != 10 || first[3] != 0xFF {
		t.Errorf("pixel(0,0) = %v, want [30 20 10 255]", first)
	}
	second := img.Pix[4:8]
	if second[0] != 70 || second[1] != 60 || second[2] != 50 {
		t.Errorf("pixel(1,0) = %v, want [70 60 50 ...]", second)
	}
	third := img.Pix[8:12]
	if third[0] != 110 || third[1] != 100 || third[2] != 90 {
		t.Errorf("pixel(0,1) = %v, want [110 100 90 ...]", third)
	}
}

func solidImage(width, height int, r, g, b uint8) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = r
		img.Pix[i+1] = g
		img.Pix[i+2] = b
		img.Pix[i+3] = 0xFF
	}
	return img
}

func TestCaptureLifecycleWithFakeGrab(t *testing.T) {
	grabs := 0
	grab := func() (*image.RGBA, error) {
		grabs++
		return solidImage(640, 480, 12, 34, 56), nil
	}
	backend, err := newCapture(grab, nil)
	if err != nil {
		t.Fatal(err)
	}
	if grabs != 1 {
		t.Errorf("constructor grabs = %d, want 1 (Python learns the desktop size)", grabs)
	}
	if w, h := backend.Size(); w != 640 || h != 480 {
		t.Errorf("Size = %dx%d, want 640x480", w, h)
	}
	if _, _, ok := backend.CursorPosition(); ok {
		t.Error("missing cursor hook must report no position, like the Python base class")
	}

	if err := backend.SetTargetSize(320, 240); err != nil {
		t.Fatal(err)
	}
	frame, err := backend.Capture()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Width() != 640 || frame.Height() != 480 {
		t.Errorf("desktop coordinates = %dx%d, want 640x480", frame.Width(), frame.Height())
	}
	if frame.Image.Rect.Dx() != 320 || frame.Image.Rect.Dy() != 240 {
		t.Errorf("capture image = %dx%d, want target 320x240",
			frame.Image.Rect.Dx(), frame.Image.Rect.Dy())
	}
	if len(frame.ContentDigest) != 8 {
		t.Errorf("digest = %d bytes, want blake2s-8", len(frame.ContentDigest))
	}

	if err := backend.SetTargetSize(0, 100); err == nil {
		t.Error("target size 0 must be rejected")
	}
	if err := backend.SetTargetSize(16385, 100); err == nil {
		t.Error("target size above 16384 must be rejected")
	}
}

func TestCapturePropagatesGrabFailure(t *testing.T) {
	failing := func() (*image.RGBA, error) {
		return nil, errGrabFailed
	}
	if _, err := newCapture(failing, nil); err == nil {
		t.Fatal("constructor must surface the grab error (permission denial)")
	}
}

var errGrabFailed = &grabError{"denied"}

type grabError struct{ message string }

func (e *grabError) Error() string { return e.message }
