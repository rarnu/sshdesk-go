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
// the renderer's smaller target size.
type Frame struct {
	Image         *image.RGBA
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
	return 0
}

func (f *Frame) Height() int {
	if f.DesktopHeight > 0 {
		return f.DesktopHeight
	}
	if f.Image != nil {
		return f.Image.Rect.Dy()
	}
	return 0
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
