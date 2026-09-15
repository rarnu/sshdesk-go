// Package quartz implements macOS input injection through Quartz CGEvent,
// mirroring the Python MacOSInput. The event construction logic is portable;
// only the cgo poster and the trust check are Darwin-only.
package quartz

import (
	"github.com/rarnu/sshdesk-go/internal/input"
)

// CGEventFlags masks (stable CoreGraphics ABI values for
// kCGEventFlagMaskShift / Control / Alternate).
const (
	flagMaskShift     = 0x20000
	flagMaskControl   = 0x40000
	flagMaskAlternate = 0x80000
)

// CGEventType mouse kinds (stable CoreGraphics ABI values).
const (
	leftMouseDown     = 1
	leftMouseUp       = 2
	rightMouseDown    = 3
	rightMouseUp      = 4
	mouseMoved        = 5
	leftMouseDragged  = 6
	rightMouseDragged = 7
	otherMouseDown    = 25
	otherMouseUp      = 26
)

// macKeycodes maps SSHDESK key codes to macOS virtual key codes, matching
// the Python MAC_KEYCODES table entry for entry.
var macKeycodes = map[input.KeyCode]uint16{
	input.KeyEnter:     36,
	input.KeyTab:       48,
	input.KeyBackspace: 51,
	input.KeyEscape:    53,
	input.KeyHome:      115,
	input.KeyEnd:       119,
	input.KeyPageUp:    116,
	input.KeyPageDown:  121,
	input.KeyDelete:    117,
	input.KeyLeft:      123,
	input.KeyRight:     124,
	input.KeyDown:      125,
	input.KeyUp:        126,
	input.KeyF1:        122,
	input.KeyF2:        120,
	input.KeyF3:        99,
	input.KeyF4:        118,
	input.KeyF5:        96,
	input.KeyF6:        97,
	input.KeyF7:        98,
	input.KeyF8:        100,
	input.KeyF9:        101,
	input.KeyF10:       109,
	input.KeyF11:       103,
	input.KeyF12:       111,
}

// poster is the cgo seam: the Darwin build posts real CGEvents, tests post
// to a recorder.
type poster interface {
	postKeyboardEvent(keyCode uint16, down bool, text string, flags uint64)
	postMouseEvent(kind int32, x, y float64, button int32)
	postScrollEvent(lines int32)
}

// Backend injects input into the macOS desktop session.
type Backend struct {
	poster         poster
	width          int
	height         int
	positionX      float64
	positionY      float64
	pressedButtons map[int]bool
	pressedKeys    map[uint16]bool
}

func newBackend(p poster, width, height int) *Backend {
	return &Backend{
		poster:         p,
		width:          width,
		height:         height,
		pressedButtons: make(map[int]bool),
		pressedKeys:    make(map[uint16]bool),
	}
}

// flagsForModifiers maps SSHDESK modifiers onto CGEventFlags masks.
func flagsForModifiers(modifiers input.Modifiers) uint64 {
	var flags uint64
	if modifiers&input.ModCtrl != 0 {
		flags |= flagMaskControl
	}
	if modifiers&input.ModAlt != 0 {
		flags |= flagMaskAlternate
	}
	if modifiers&input.ModShift != 0 {
		flags |= flagMaskShift
	}
	return flags
}

// Key injects one normalized key event: press, release, or tap (press +
// release), mirroring the Python action handling.
func (b *Backend) Key(event input.KeyEvent) {
	if event.Action < 0 || event.Action > 2 {
		return
	}
	code, known := macKeycodes[event.Code]
	text := ""
	if event.Code == input.KeyCharacter {
		if event.Unicode != 0 {
			text = string(event.Unicode)
		}
	} else if !known {
		return
	}
	presses := []bool{true, false}
	if event.Action == input.KeyPress {
		presses = presses[:1]
	} else if event.Action == input.KeyRelease {
		presses = presses[1:]
	}
	for _, down := range presses {
		b.poster.postKeyboardEvent(code, down, text, flagsForModifiers(event.Modifiers))
	}
	if event.Action == input.KeyPress {
		b.pressedKeys[code] = true
	} else {
		delete(b.pressedKeys, code)
	}
}

// point clamps desktop coordinates into the main display bounds.
func (b *Backend) point(x, y int) (float64, float64) {
	return float64(min(max(0, x), b.width-1)), float64(min(max(0, y), b.height-1))
}

// Move injects pointer motion, switching to a drag kind while a button is
// held.
func (b *Backend) Move(x, y int) {
	b.positionX, b.positionY = b.point(x, y)
	kind := int32(mouseMoved)
	if b.pressedButtons[1] {
		kind = leftMouseDragged
	} else if b.pressedButtons[3] {
		kind = rightMouseDragged
	}
	b.poster.postMouseEvent(kind, b.positionX, b.positionY, 0)
}

// Button injects a left/middle/right button transition.
func (b *Backend) Button(button int, pressed bool, x, y int) {
	if button < 1 || button > 3 {
		return
	}
	b.positionX, b.positionY = b.point(x, y)
	mouseButton := map[int]int32{1: 0, 2: 2, 3: 1}[button]
	down := map[int]int32{1: leftMouseDown, 2: otherMouseDown, 3: rightMouseDown}
	up := map[int]int32{1: leftMouseUp, 2: otherMouseUp, 3: rightMouseUp}
	kind := down[button]
	if !pressed {
		kind = up[button]
	}
	b.poster.postMouseEvent(kind, b.positionX, b.positionY, mouseButton)
	if pressed {
		b.pressedButtons[button] = true
	} else {
		delete(b.pressedButtons, button)
	}
}

// Scroll moves the pointer and injects a line-unit scroll wheel event
// clamped to ±20 lines.
func (b *Backend) Scroll(amount, x, y int) {
	b.Move(x, y)
	b.poster.postScrollEvent(int32(max(-20, min(20, amount))))
}

// Close releases every held button and key so a dropped session cannot
// wedge the desktop.
func (b *Backend) Close() {
	for button := range b.pressedButtons {
		b.Button(button, false, int(b.positionX), int(b.positionY))
	}
	for code := range b.pressedKeys {
		b.poster.postKeyboardEvent(code, false, "", 0)
	}
	clear(b.pressedButtons)
	clear(b.pressedKeys)
}
