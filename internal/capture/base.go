// Package capture defines the desktop frame contract and the screen capture
// backend interface.
package capture

import (
	"fmt"
	"image"

	"golang.org/x/crypto/blake2s"
)

// Frame is an immutable RGB desktop frame. DesktopWidth/DesktopHeight
// preserve the remote coordinate space when a backend captures directly at
// the renderer's smaller target size. Pixels arrive either as Image (RGBA)
// or as packed RGB24 in RGB with RGBWidth/RGBHeight; hot paths should
// consume whichever form is present instead of converting.
type Frame struct {
	Image         *image.RGBA
	RGB           []byte
	RGBWidth      int
	RGBHeight     int
	CapturedNs    int64
	DesktopWidth  int
	DesktopHeight int
	ContentDigest []byte
}

func (f *Frame) Width() int {
	if f.DesktopWidth > 0 {
		return f.DesktopWidth
	}
	if f.Image != nil {
		return f.Image.Rect.Dx()
	}
	return f.RGBWidth
}

func (f *Frame) Height() int {
	if f.DesktopHeight > 0 {
		return f.DesktopHeight
	}
	if f.Image != nil {
		return f.Image.Rect.Dy()
	}
	return f.RGBHeight
}

// RGBAImage returns the frame pixels as an RGBA image, expanding packed
// RGB24 when the backend delivered that form. It allocates on the RGB24
// path, so per-frame consumers should branch on the raw fields instead;
// this is for rare uses like screenshots.
func (f *Frame) RGBAImage() *image.RGBA {
	if f.Image != nil {
		return f.Image
	}
	return RGB24ToRGBA(f.RGB, f.RGBWidth, f.RGBHeight)
}

// RGB24ToRGBA expands packed RGB24 pixels into an RGBA image.
func RGB24ToRGBA(rgb []byte, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height && i*3+2 < len(rgb); i++ {
		s, d := i*3, i*4
		img.Pix[d] = rgb[s]
		img.Pix[d+1] = rgb[s+1]
		img.Pix[d+2] = rgb[s+2]
		img.Pix[d+3] = 0xFF
	}
	return img
}

// DigestPixels returns the blake2s-8 content digest of raw RGB24 pixels.
func DigestPixels(pixels []byte) []byte {
	sum := blake2s.Sum256(pixels)
	digest := make([]byte, 8)
	copy(digest, sum[:8])
	return digest
}

// ScreenCapture produces desktop frames in RGB format.
type ScreenCapture interface {
	Capture() (*Frame, error)
	Size() (int, int)
	CursorPosition() (int, int, bool)
	SetTargetSize(width, height int) error
	SetFrameRate(framesPerSecond float64) error
	Close()
}

// ValidateTargetSize enforces the shared capture target bounds.
func ValidateTargetSize(width, height int) error {
	if width < 1 || width > 16384 || height < 1 || height > 16384 {
		return errTargetSize
	}
	return nil
}

// ValidateFrameRate enforces the shared capture pacing bounds.
func ValidateFrameRate(fps float64) error {
	if fps < 0.5 || fps > 120.0 {
		return errFrameRate
	}
	return nil
}

// Name reports the backend's Python-style class name for status output,
// falling back to the Go type name when the backend does not declare one.
func Name(c ScreenCapture) string {
	if named, ok := c.(interface{ BackendName() string }); ok {
		return named.BackendName()
	}
	return fmt.Sprintf("%T", c)
}
