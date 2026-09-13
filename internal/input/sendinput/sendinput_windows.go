//go:build windows

package sendinput

import (
	"syscall"
	"unsafe"
)

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procSendInput        = user32.NewProc("SendInput")
	procGetSystemMetrics = user32.NewProc("GetSystemMetrics")
)

// mouseRecord mirrors INPUT with the MOUSEINPUT union member (40 bytes on
// x64: type, alignment padding, then the 32-byte union slot).
type mouseRecord struct {
	kind  uint32
	_     uint32
	dx    int32
	dy    int32
	data  uint32
	flags uint32
	time  uint32
	extra uintptr
}

// keyboardRecord mirrors INPUT with the KEYBDINPUT union member, padded to
// the same 40-byte record size.
type keyboardRecord struct {
	kind  uint32
	_     uint32
	vk    uint16
	scan  uint16
	flags uint32
	time  uint32
	extra uintptr
	_     [8]byte
}

type winSender struct{}

func (winSender) sendMouse(value mouseInput) error {
	record := mouseRecord{
		kind:  inputMouse,
		dx:    value.dx,
		dy:    value.dy,
		data:  value.data,
		flags: value.flags,
	}
	sent, _, err := procSendInput.Call(
		1, uintptr(unsafe.Pointer(&record)), unsafe.Sizeof(record))
	if sent != 1 {
		return err
	}
	return nil
}

func (winSender) sendKeyboard(value keyStroke) error {
	record := keyboardRecord{
		kind:  inputKeyboard,
		vk:    value.vk,
		scan:  value.scan,
		flags: value.flags,
	}
	sent, _, err := procSendInput.Call(
		1, uintptr(unsafe.Pointer(&record)), unsafe.Sizeof(record))
	if sent != 1 {
		return err
	}
	return nil
}

func getSystemMetrics(index int) int {
	ret, _, _ := procGetSystemMetrics.Call(uintptr(index))
	return int(int32(ret))
}

// New builds the SendInput backend with the virtual desktop dimensions.
func New() (*Backend, error) {
	width := max(1, getSystemMetrics(smCXVirtualScreen))
	height := max(1, getSystemMetrics(smCYVirtualScreen))
	return newBackend(winSender{}, width, height), nil
}
