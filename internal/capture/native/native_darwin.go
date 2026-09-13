//go:build darwin

package native

/*
#cgo CFLAGS: -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#import <CoreGraphics/CoreGraphics.h>
#import <CoreFoundation/CoreFoundation.h>
*/
import "C"

import (
	"errors"
	"image"
	"unsafe"

	"github.com/rylena/sshdesk-go/internal/capture/xshm"
)

// grabDisplay captures the main display through Quartz and scales it to the
// logical (points) resolution so Retina 2x backing stores match the desktop
// coordinate space.
func grabDisplay() (*image.RGBA, error) {
	display := C.CGMainDisplayID()
	cgimage := C.CGDisplayCreateImage(display)
	if cgimage == 0 {
		return nil, errors.New(
			"desktop capture failed; grant Screen Recording permission to the SSH/Python process")
	}
	defer C.CGImageRelease(cgimage)
	width := int(C.CGImageGetWidth(cgimage))
	height := int(C.CGImageGetHeight(cgimage))
	bytesPerRow := int(C.CGImageGetBytesPerRow(cgimage))
	provider := C.CGImageGetDataProvider(cgimage)
	var copied C.CFDataRef
	if provider != 0 {
		copied = C.CGDataProviderCopyData(provider)
	}
	if width < 1 || height < 1 || copied == 0 {
		return nil, errors.New("desktop capture failed; empty display image")
	}
	defer C.CFRelease(C.CFTypeRef(copied))
	length := int(C.CFDataGetLength(copied))
	if length < bytesPerRow*height {
		return nil, errors.New("desktop capture failed; incomplete pixel buffer")
	}
	raw := C.GoBytes(unsafe.Pointer(C.CFDataGetBytePtr(copied)), C.int(length))
	img := bgraToRGBA(raw, width, height, bytesPerRow)
	logicalW := int(C.CGDisplayPixelsWide(display))
	logicalH := int(C.CGDisplayPixelsHigh(display))
	if logicalW >= 1 && logicalH >= 1 && (width != logicalW || height != logicalH) {
		// CatmullRom stands in for Pillow's BICUBIC (existing approximation).
		img = xshm.Scale(img, logicalW, logicalH)
	}
	return img, nil
}

// cursorPosition reads the global pointer location through a transient
// CGEvent, the Go equivalent of CGEventGetLocation.
func cursorPosition() (int, int, bool) {
	event := C.CGEventCreate(0)
	if event == 0 {
		return 0, 0, false
	}
	defer C.CFRelease(C.CFTypeRef(event))
	point := C.CGEventGetLocation(event)
	return int(point.x), int(point.y), true
}

// New builds the Quartz native capture.
func New() (*Capture, error) {
	return newCapture(grabDisplay, cursorPosition)
}
