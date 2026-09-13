//go:build windows

package native

import (
	"errors"
	"image"
	"syscall"
	"unsafe"
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
	srccopy           = 0x00CC0020
	dibRGBColors      = 0
	biRGB             = 0
)

var errNotInteractive = errors.New(
	"desktop capture failed; run SSHDESK in the logged-in interactive Windows session")

var (
	user32                     = syscall.NewLazyDLL("user32.dll")
	gdi32                      = syscall.NewLazyDLL("gdi32.dll")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetDC                  = user32.NewProc("GetDC")
	procReleaseDC              = user32.NewProc("ReleaseDC")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procBitBlt                 = gdi32.NewProc("BitBlt")
	procGetDIBits              = gdi32.NewProc("GetDIBits")
)

type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

func getSystemMetrics(index int) int {
	ret, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(ret))
}

// grabVirtualScreen captures the whole virtual desktop across all monitors,
// standing in for Pillow's ImageGrab.grab(all_screens=True).
func grabVirtualScreen() (*image.RGBA, error) {
	left := getSystemMetrics(smXVirtualScreen)
	top := getSystemMetrics(smYVirtualScreen)
	width := getSystemMetrics(smCXVirtualScreen)
	height := getSystemMetrics(smCYVirtualScreen)
	if width < 1 || height < 1 {
		return nil, errNotInteractive
	}
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return nil, errNotInteractive
	}
	defer procReleaseDC.Call(0, screen)
	memory, _, _ := procCreateCompatibleDC.Call(screen)
	if memory == 0 {
		return nil, errNotInteractive
	}
	defer procDeleteDC.Call(memory)
	bitmap, _, _ := procCreateCompatibleBitmap.Call(screen, uintptr(width), uintptr(height))
	if bitmap == 0 {
		return nil, errNotInteractive
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, _ := procSelectObject.Call(memory, bitmap)
	copied, _, _ := procBitBlt.Call(
		memory, 0, 0, uintptr(width), uintptr(height),
		screen, uintptr(uint32(int32(left))), uintptr(uint32(int32(top))), srccopy)
	procSelectObject.Call(memory, previous)
	if copied == 0 {
		return nil, errNotInteractive
	}
	header := bitmapInfoHeader{
		size:        40,
		width:       int32(width),
		height:      -int32(height), // top-down rows
		planes:      1,
		bitCount:    32,
		compression: biRGB,
	}
	buffer := make([]byte, width*height*4)
	lines, _, _ := procGetDIBits.Call(
		memory, bitmap, 0, uintptr(height),
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(unsafe.Pointer(&header)), dibRGBColors)
	if lines == 0 {
		return nil, errNotInteractive
	}
	return bgraToRGBA(buffer, width, height, width*4), nil
}

// New builds the BitBlt native capture. Cursor reporting stays unavailable,
// matching the Python NativeCapture.
func New() (*Capture, error) {
	return newCapture(grabVirtualScreen, nil)
}
