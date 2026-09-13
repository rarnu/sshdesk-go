// Package xshm implements the MIT-SHM X11 capture layer. The pixel
// conversion is platform-neutral so tests run without an X server; the
// shared-memory and X protocol wiring is Linux-only.
package xshm

import (
	"fmt"
	"image"

	"golang.org/x/image/draw"
)

// PixelLayout is the validated BGRX channel layout of the root visual.
type PixelLayout struct {
	BitsPerPixel int
	RedMask      uint32
	GreenMask    uint32
	BlueMask     uint32
}

// Validate enforces the 32bpp BGRX layout the converter assumes, mirroring
// the Python XShmCreateImage mask check.
func (l PixelLayout) Validate() error {
	if l.BitsPerPixel != 32 || l.RedMask != 0xFF0000 || l.GreenMask != 0x00FF00 || l.BlueMask != 0x0000FF {
		return fmt.Errorf("unsupported X11 pixel layout for accelerated capture")
	}
	return nil
}

// BGRXToRGB converts one shared-memory image (BGRX byte order, possibly
// padded rows) into a packed RGBA image.
func BGRXToRGB(src []byte, width, height, bytesPerLine int) (*image.RGBA, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("X11 desktop dimensions must be positive")
	}
	if bytesPerLine < width*4 {
		return nil, fmt.Errorf("X11 shared image stride %d is smaller than the width", bytesPerLine)
	}
	if len(src) < bytesPerLine*(height-1)+width*4 {
		return nil, fmt.Errorf("X11 shared image buffer is too small")
	}
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		srcRow := y * bytesPerLine
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
	return dst, nil
}

// Scale downscales img to the target with a Catmull-Rom kernel, standing in
// for Pillow's LANCZOS/BICUBIC resampling.
func Scale(img *image.RGBA, targetWidth, targetHeight int) *image.RGBA {
	if img.Rect.Dx() == targetWidth && img.Rect.Dy() == targetHeight {
		return img
	}
	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(dst, dst.Rect, img, img.Rect, draw.Src, nil)
	return dst
}
