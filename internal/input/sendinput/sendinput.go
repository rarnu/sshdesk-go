// Package sendinput implements Windows desktop input injection through
// user32.SendInput, mirroring the Python WindowsInput. The event sequence
// logic is portable; only the SendInput call and the virtual screen metrics
// are Windows-only.
package sendinput

import (
	"math"
	"unicode/utf16"

	"github.com/rarnu/sshdesk-go/internal/input"
)

const (
	inputMouse    = 0
	inputKeyboard = 1

	keyeventfKeyup   = 0x0002
	keyeventfUnicode = 0x0004

	mouseeventfMove        = 0x0001
	mouseeventfLeftDown    = 0x0002
	mouseeventfLeftUp      = 0x0004
	mouseeventfRightDown   = 0x0008
	mouseeventfRightUp     = 0x0010
	mouseeventfMiddleDown  = 0x0020
	mouseeventfMiddleUp    = 0x0040
	mouseeventfWheel       = 0x0800
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000

	wheelDelta = 120

	vkBackspace = 0x08
	vkTab       = 0x09
	vkEnter     = 0x0D
	vkEscape    = 0x1B
	vkPageUp    = 0x21
	vkPageDown  = 0x22
	vkEnd       = 0x23
	vkHome      = 0x24
	vkLeft      = 0x25
	vkUp        = 0x26
	vkRight     = 0x27
	vkDown      = 0x28
	vkInsert    = 0x2D
	vkDelete    = 0x2E

	vkShift   = 0x10
	vkControl = 0x11
	vkAlt     = 0x12
)

// vkKeys maps SSHDESK key codes to Windows virtual key codes, matching the
// Python VK_KEYS table (F1-F12 = 0x70-0x7B).
var vkKeys = map[input.KeyCode]uint16{
	input.KeyBackspace: vkBackspace,
	input.KeyTab:       vkTab,
	input.KeyEnter:     vkEnter,
	input.KeyEscape:    vkEscape,
	input.KeyPageUp:    vkPageUp,
	input.KeyPageDown:  vkPageDown,
	input.KeyEnd:       vkEnd,
	input.KeyHome:      vkHome,
	input.KeyLeft:      vkLeft,
	input.KeyUp:        vkUp,
	input.KeyRight:     vkRight,
	input.KeyDown:      vkDown,
	input.KeyInsert:    vkInsert,
	input.KeyDelete:    vkDelete,
	input.KeyF1:        0x70,
	input.KeyF2:        0x71,
	input.KeyF3:        0x72,
	input.KeyF4:        0x73,
	input.KeyF5:        0x74,
	input.KeyF6:        0x75,
	input.KeyF7:        0x76,
	input.KeyF8:        0x77,
	input.KeyF9:        0x78,
	input.KeyF10:       0x79,
	input.KeyF11:       0x7A,
	input.KeyF12:       0x7B,
}

// keyStroke is one KEYBDINPUT payload.
type keyStroke struct {
	vk    uint16
	scan  uint16
	flags uint32
}

// mouseInput is one MOUSEINPUT payload.
type mouseInput struct {
	dx    int32
	dy    int32
	data  uint32
	flags uint32
}

// sender is the SendInput seam: the Windows build sends real INPUT records,
// tests record them.
type sender interface {
	sendMouse(value mouseInput) error
	sendKeyboard(value keyStroke) error
}

// Backend injects input into the interactive Windows desktop session.
type Backend struct {
	sender         sender
	width          int
	height         int
	pressedButtons map[int]bool
	pressedKeys    map[uint16]bool
}

func newBackend(s sender, width, height int) *Backend {
	return &Backend{
		sender:         s,
		width:          width,
		height:         height,
		pressedButtons: make(map[int]bool),
		pressedKeys:    make(map[uint16]bool),
	}
}

// modifierVKs maps SSHDESK modifiers onto virtual key codes in the Python
// order (CTRL, ALT, SHIFT).
func modifierVKs(modifiers input.Modifiers) []uint16 {
	var vks []uint16
	if modifiers&input.ModCtrl != 0 {
		vks = append(vks, vkControl)
	}
	if modifiers&input.ModAlt != 0 {
		vks = append(vks, vkAlt)
	}
	if modifiers&input.ModShift != 0 {
		vks = append(vks, vkShift)
	}
	return vks
}

// utf16Units encodes a character into UTF-16LE code units (surrogate pairs
// split), matching the Python encoding loop.
func utf16Units(value rune) []uint16 {
	return utf16.Encode([]rune{value})
}

// Key injects one normalized key event with the Python modifier ordering:
// modifiers down, main key, main key up (tap), modifiers up in reverse.
func (b *Backend) Key(event input.KeyEvent) {
	if event.Action < 0 || event.Action > 2 {
		return
	}
	modifiers := modifierVKs(event.Modifiers)
	if event.Action == input.KeyPress || event.Action == input.KeyTap {
		for _, modifier := range modifiers {
			_ = b.sender.sendKeyboard(keyStroke{vk: modifier})
			if event.Action == input.KeyPress {
				b.pressedKeys[modifier] = true
			}
		}
	}
	var flags uint32
	if event.Action == input.KeyRelease {
		flags = keyeventfKeyup
	}
	if event.Code == input.KeyCharacter && event.Unicode > 0 && event.Unicode <= 0x10FFFF {
		for _, unit := range utf16Units(event.Unicode) {
			_ = b.sender.sendKeyboard(keyStroke{scan: unit, flags: keyeventfUnicode | flags})
			if event.Action == input.KeyTap {
				_ = b.sender.sendKeyboard(keyStroke{scan: unit, flags: keyeventfUnicode | keyeventfKeyup})
			}
		}
	} else {
		vk, known := vkKeys[event.Code]
		if !known {
			if event.Action == input.KeyRelease || event.Action == input.KeyTap {
				b.releaseModifiers(modifiers)
			}
			return
		}
		_ = b.sender.sendKeyboard(keyStroke{vk: vk, flags: flags})
		if event.Action == input.KeyTap {
			_ = b.sender.sendKeyboard(keyStroke{vk: vk, flags: keyeventfKeyup})
		}
		if event.Action == input.KeyPress {
			b.pressedKeys[vk] = true
		} else {
			delete(b.pressedKeys, vk)
		}
	}
	if event.Action == input.KeyRelease || event.Action == input.KeyTap {
		b.releaseModifiers(modifiers)
	}
}

func (b *Backend) releaseModifiers(modifiers []uint16) {
	for index := len(modifiers) - 1; index >= 0; index-- {
		_ = b.sender.sendKeyboard(keyStroke{vk: modifiers[index], flags: keyeventfKeyup})
		delete(b.pressedKeys, modifiers[index])
	}
}

// normalizeAxis scales a desktop coordinate onto the 0..65535 absolute range
// using Python round() (banker's rounding).
func normalizeAxis(value, size int) int32 {
	clamped := min(max(0, value), size-1)
	return int32(math.RoundToEven(float64(clamped) * 65535 / float64(max(1, size-1))))
}

// mouse posts one absolute virtual-desktop mouse record.
func (b *Backend) mouse(x, y int, flags uint32, data uint32) error {
	absoluteX := normalizeAxis(x, b.width)
	absoluteY := normalizeAxis(y, b.height)
	flags |= mouseeventfAbsolute | mouseeventfVirtualDesk
	return b.sender.sendMouse(mouseInput{dx: absoluteX, dy: absoluteY, data: data, flags: flags})
}

// Move injects absolute pointer motion.
func (b *Backend) Move(x, y int) {
	_ = b.mouse(x, y, mouseeventfMove, 0)
}

// Button injects a left/middle/right button transition.
func (b *Backend) Button(button int, pressed bool, x, y int) {
	if button < 1 || button > 3 {
		return
	}
	b.mouseButton(button, pressed, x, y)
}

func (b *Backend) mouseButton(button int, pressed bool, x, y int) {
	var flags uint32
	switch button {
	case 1:
		if pressed {
			flags = mouseeventfLeftDown
		} else {
			flags = mouseeventfLeftUp
		}
	case 2:
		if pressed {
			flags = mouseeventfMiddleDown
		} else {
			flags = mouseeventfMiddleUp
		}
	case 3:
		if pressed {
			flags = mouseeventfRightDown
		} else {
			flags = mouseeventfRightUp
		}
	}
	_ = b.mouse(x, y, mouseeventfMove|flags, 0)
	if pressed {
		b.pressedButtons[button] = true
	} else {
		delete(b.pressedButtons, button)
	}
}

// wheelData converts a line amount into the signed wheel delta bit pattern
// (clamp ±20, ×120), matching ctypes.c_ulong(amount * WHEEL_DELTA).
func wheelData(amount int) uint32 {
	return uint32(int32(max(-20, min(20, amount)) * wheelDelta))
}

// Scroll injects a wheel event at the given pointer position.
func (b *Backend) Scroll(amount, x, y int) {
	_ = b.mouse(x, y, mouseeventfMove|mouseeventfWheel, wheelData(amount))
}

// Close releases every held button and key.
func (b *Backend) Close() {
	for button := range b.pressedButtons {
		b.mouseButton(button, false, 0, 0)
	}
	for vk := range b.pressedKeys {
		_ = b.sender.sendKeyboard(keyStroke{vk: vk, flags: keyeventfKeyup})
	}
	clear(b.pressedButtons)
	clear(b.pressedKeys)
}
