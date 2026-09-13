//go:build darwin

package quartz

/*
#cgo CFLAGS: -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices -framework CoreFoundation
#import <CoreGraphics/CoreGraphics.h>
#import <ApplicationServices/ApplicationServices.h>
#import <CoreFoundation/CoreFoundation.h>

// cgo cannot call the variadic CGEventCreateScrollWheelEvent directly.
static CGEventRef sshdesk_scroll_event(int32_t wheel) {
    return CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitLine, 1, wheel);
}
*/
import "C"

import (
	"errors"
	"unicode/utf16"
	"unsafe"
)

// cgPoster posts real CGEvents to the HID event tap.
type cgPoster struct{}

func (cgPoster) postKeyboardEvent(keyCode uint16, down bool, text string, flags uint64) {
	event := C.CGEventCreateKeyboardEvent(0, C.CGKeyCode(keyCode), C.bool(down))
	if event == 0 {
		return
	}
	defer C.CFRelease(C.CFTypeRef(event))
	if text != "" {
		units := utf16.Encode([]rune(text))
		C.CGEventKeyboardSetUnicodeString(
			event, C.UniCharCount(len(units)), (*C.UniChar)(unsafe.Pointer(&units[0])))
	}
	C.CGEventSetFlags(event, C.CGEventFlags(flags))
	C.CGEventPost(C.kCGHIDEventTap, event)
}

func (cgPoster) postMouseEvent(kind int32, x, y float64, button int32) {
	point := C.CGPointMake(C.double(x), C.double(y))
	event := C.CGEventCreateMouseEvent(0, C.CGEventType(kind), point, C.CGMouseButton(button))
	if event == 0 {
		return
	}
	defer C.CFRelease(C.CFTypeRef(event))
	C.CGEventPost(C.kCGHIDEventTap, event)
}

func (cgPoster) postScrollEvent(lines int32) {
	event := C.sshdesk_scroll_event(C.int32_t(lines))
	if event == 0 {
		return
	}
	defer C.CFRelease(C.CFTypeRef(event))
	C.CGEventPost(C.kCGHIDEventTap, event)
}

// processIsTrusted wraps the Accessibility trust check. Newer PyObjC builds
// no longer export AXIsProcessTrusted on the Quartz module and Python falls
// back to the ApplicationServices C API; cgo calls that same API directly.
func processIsTrusted() bool {
	return C.AXIsProcessTrusted() != 0
}

// New builds the Quartz input backend after the Accessibility permission
// check, mirroring the Python constructor.
func New() (*Backend, error) {
	if !processIsTrusted() {
		return nil, errors.New(
			"grant Accessibility permission to the SSH/Python process for input control")
	}
	display := C.CGMainDisplayID()
	width := int(C.CGDisplayPixelsWide(display))
	height := int(C.CGDisplayPixelsHigh(display))
	return newBackend(cgPoster{}, width, height), nil
}
