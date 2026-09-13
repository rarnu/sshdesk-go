// Package native implements the desktop capture for Windows (BitBlt) and
// macOS (Quartz), mirroring the Python NativeCapture.
package native

import (
	"image"
	"time"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/xshm"
)

// Capture grabs the desktop through the OS-native API. The grab and cursor
// hooks are platform-specific; everything else mirrors the shared Python
// NativeCapture logic.
type Capture struct {
	grab     func() (*image.RGBA, error)
	cursorFn func() (int, int, bool)

	targetSet bool
	targetW   int
	targetH   int
	desktopW  int
	desktopH  int
}

// newCapture validates the backend by grabbing the first frame, which also
// learns the desktop size like the Python constructor.
func newCapture(grab func() (*image.RGBA, error), cursorFn func() (int, int, bool)) (*Capture, error) {
	c := &Capture{grab: grab, cursorFn: cursorFn}
	img, err := grab()
	if err != nil {
		return nil, err
	}
	c.desktopW = img.Rect.Dx()
	c.desktopH = img.Rect.Dy()
	return c, nil
}

// BackendName reports the Python-style class name for status output.
func (c *Capture) BackendName() string { return "NativeCapture" }

// Size returns the desktop dimensions seen by the latest grab.
func (c *Capture) Size() (int, int) { return c.desktopW, c.desktopH }

// SetTargetSize sets the capture image size; the desktop coordinate space is
// preserved on the frame.
func (c *Capture) SetTargetSize(width, height int) error {
	if err := capture.ValidateTargetSize(width, height); err != nil {
		return err
	}
	c.targetSet = true
	c.targetW = width
	c.targetH = height
	return nil
}

// SetFrameRate is accepted for interface conformance; native capture is
// on-demand like the Python implementation.
func (c *Capture) SetFrameRate(float64) error { return nil }

// Capture grabs one desktop frame, scales it to the target size, and stamps
// the blake2s-8 content digest of the RGB pixels.
func (c *Capture) Capture() (*capture.Frame, error) {
	img, err := c.grab()
	if err != nil {
		return nil, err
	}
	desktopW := img.Rect.Dx()
	desktopH := img.Rect.Dy()
	c.desktopW = desktopW
	c.desktopH = desktopH
	if c.targetSet && (desktopW != c.targetW || desktopH != c.targetH) {
		img = xshm.Scale(img, c.targetW, c.targetH)
	}
	return &capture.Frame{
		Image:         img,
		CapturedNs:    time.Now().UnixNano(),
		DesktopWidth:  desktopW,
		DesktopHeight: desktopH,
		ContentDigest: capture.DigestPixels(rgb24(img)),
	}, nil
}

// CursorPosition reports the pointer location when the platform hook
// provides one; the Python NativeCapture reports None (here false).
func (c *Capture) CursorPosition() (int, int, bool) {
	if c.cursorFn == nil {
		return 0, 0, false
	}
	return c.cursorFn()
}

// Close releases no resources; native capture holds none.
func (c *Capture) Close() {}

// bgraToRGBA converts one raw BGRA buffer (possibly padded rows) into a
// packed RGBA image, matching Pillow's frombuffer("BGRA").convert("RGB").
func bgraToRGBA(src []byte, width, height, bytesPerRow int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcRow := y * bytesPerRow
		dstRow := y * dst.Stride
		for x := 0; x < width; x++ {
			s := srcRow + x*4
			d := dstRow + x*4
			dst.Pix[d] = src[s+2]   // R
			dst.Pix[d+1] = src[s+1] // G
			dst.Pix[d+2] = src[s]   // B
			dst.Pix[d+3] = 0xFF
		}
	}
	return dst
}

// rgb24 packs the image into RGB24 bytes for the content digest.
func rgb24(img *image.RGBA) []byte {
	width := img.Rect.Dx()
	height := img.Rect.Dy()
	out := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		row := img.PixOffset(0, y)
		for x := 0; x < width; x++ {
			s := row + x*4
			d := (y*width + x) * 3
			out[d] = img.Pix[s]
			out[d+1] = img.Pix[s+1]
			out[d+2] = img.Pix[s+2]
		}
	}
	return out
}
