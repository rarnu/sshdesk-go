// Package mutter injects input directly into a linked GNOME RemoteDesktop
// session over D-Bus, mirroring the Python MutterInput. The keysym tables,
// button codes, coordinate clamping, and call sequences are pure logic; the
// D-Bus caller is an injected seam (godbus in production, a recorder in
// tests), so the package compiles and is fully testable everywhere.
package mutter

import (
	"github.com/rarnu/sshdesk-go/internal/input"
)

// D-Bus names, mirroring the Python class constants.
const (
	RemoteName       = "org.gnome.Mutter.RemoteDesktop"
	SessionInterface = "org.gnome.Mutter.RemoteDesktop.Session"
)

// SpecialKeysyms maps normalized key codes to X11 keysyms (verbatim from
// the Python SPECIAL_KEYSYMS table).
var SpecialKeysyms = map[input.KeyCode]uint32{
	input.KeyEscape:    0xFF1B,
	input.KeyBackspace: 0xFF08,
	input.KeyTab:       0xFF09,
	input.KeyEnter:     0xFF0D,
	input.KeyHome:      0xFF50,
	input.KeyLeft:      0xFF51,
	input.KeyUp:        0xFF52,
	input.KeyRight:     0xFF53,
	input.KeyDown:      0xFF54,
	input.KeyPageUp:    0xFF55,
	input.KeyPageDown:  0xFF56,
	input.KeyEnd:       0xFF57,
	input.KeyInsert:    0xFF63,
	input.KeyDelete:    0xFFFF,
	input.KeyF1:        0xFFBE,
	input.KeyF2:        0xFFBF,
	input.KeyF3:        0xFFC0,
	input.KeyF4:        0xFFC1,
	input.KeyF5:        0xFFC2,
	input.KeyF6:        0xFFC3,
	input.KeyF7:        0xFFC4,
	input.KeyF8:        0xFFC5,
	input.KeyF9:        0xFFC6,
	input.KeyF10:       0xFFC7,
	input.KeyF11:       0xFFC8,
	input.KeyF12:       0xFFC9,
}

// ModifierCodes mirrors the Python _modifier_codes: the table iterates in
// CTRL(29), SHIFT(42), ALT(56) evdev order.
func ModifierCodes(modifiers input.Modifiers) []int {
	var codes []int
	if modifiers&input.ModCtrl != 0 {
		codes = append(codes, 29)
	}
	if modifiers&input.ModShift != 0 {
		codes = append(codes, 42)
	}
	if modifiers&input.ModAlt != 0 {
		codes = append(codes, 56)
	}
	return codes
}

// ButtonCodes maps the normalized buttons to evdev BTN codes; note button
// 2 is the middle button (0x112) and button 3 the right one (0x111).
var ButtonCodes = map[int]int{1: 0x110, 2: 0x112, 3: 0x111}

// Keysym resolves the X11 keysym for one key event, mirroring the Python
// _keysym: Latin-1 characters map to themselves and other codepoints live
// in the 0x01000000 namespace.
func Keysym(event input.KeyEvent) (uint32, bool) {
	if event.Code != input.KeyCharacter {
		keysym, ok := SpecialKeysyms[event.Code]
		return keysym, ok
	}
	if event.Unicode <= 0 || event.Unicode > 0x10FFFF {
		return 0, false
	}
	if event.Unicode <= 0xFF {
		return uint32(event.Unicode), true
	}
	return 0x01000000 | uint32(event.Unicode), true
}

// BoundedPoint clamps a pointer position to the desktop, mirroring the
// Python _bounded_point (floats, upper bound width-1/height-1).
func BoundedPoint(x, y, width, height int) (float64, float64) {
	boundX := max(0, min(x, max(0, width-1)))
	boundY := max(0, min(y, max(0, height-1)))
	return float64(boundX), float64(boundY)
}

// ClampSteps bounds a scroll amount to ±20 like the Python scroll().
func ClampSteps(amount int) int {
	return max(-20, min(20, amount))
}

// Caller performs one D-Bus method call on the remote desktop session; the
// signature string is carried for test fidelity (godbus derives the wire
// signature from the values).
type Caller func(method, signature string, values ...any) error

// Input mirrors the Python MutterInput.
type Input struct {
	call        Caller
	sessionPath string
	streamPath  string
	desktopSize func() (int, int)
	cursorMoved func(int, int)

	pressedKeysyms   map[uint32]bool
	pressedModifiers map[int]bool
	pressedButtons   map[int]bool
	closed           bool
}

var _ input.Backend = (*Input)(nil)

// New wires an input backend to a remote desktop session.
func New(call Caller, sessionPath, streamPath string, desktopSize func() (int, int), cursorMoved func(int, int)) *Input {
	return &Input{
		call:             call,
		sessionPath:      sessionPath,
		streamPath:       streamPath,
		desktopSize:      desktopSize,
		cursorMoved:      cursorMoved,
		pressedKeysyms:   make(map[uint32]bool),
		pressedModifiers: make(map[int]bool),
		pressedButtons:   make(map[int]bool),
	}
}

// notify performs one call unless the backend is closed; the Backend
// interface returns no error, so D-Bus failures are dropped silently (the
// Python version would raise RuntimeError).
func (i *Input) notify(method, signature string, values ...any) {
	if i.closed {
		return
	}
	_ = i.call(method, signature, values...)
}

func (i *Input) keycode(code int, pressed bool) {
	i.notify("NotifyKeyboardKeycode", "(ub)", code, pressed)
}

func (i *Input) keysymEvent(keysym uint32, pressed bool) {
	i.notify("NotifyKeyboardKeysym", "(ub)", keysym, pressed)
}

// Key injects one normalized key event: modifiers press in order, the main
// keysym presses, releases, then the modifiers release in reverse.
func (i *Input) Key(event input.KeyEvent) {
	if event.Action != input.KeyPress && event.Action != input.KeyRelease && event.Action != input.KeyTap {
		return
	}
	keysym, ok := Keysym(event)
	if !ok {
		return
	}
	modifiers := ModifierCodes(event.Modifiers)
	if event.Action == input.KeyPress || event.Action == input.KeyTap {
		for _, code := range modifiers {
			i.keycode(code, true)
			i.pressedModifiers[code] = true
		}
		i.keysymEvent(keysym, true)
		i.pressedKeysyms[keysym] = true
	}
	if event.Action == input.KeyRelease || event.Action == input.KeyTap {
		i.keysymEvent(keysym, false)
		delete(i.pressedKeysyms, keysym)
		for index := len(modifiers) - 1; index >= 0; index-- {
			i.keycode(modifiers[index], false)
			delete(i.pressedModifiers, modifiers[index])
		}
	}
}

// Move positions the pointer and reports the cursor back to the capture.
func (i *Input) Move(x, y int) {
	width, height := i.desktopSize()
	boundedX, boundedY := BoundedPoint(x, y, width, height)
	i.notify("NotifyPointerMotionAbsolute", "(sdd)", i.streamPath, boundedX, boundedY)
	if i.cursorMoved != nil {
		i.cursorMoved(int(boundedX), int(boundedY))
	}
}

// Button moves the pointer and presses or releases one of the three primary
// buttons.
func (i *Input) Button(button int, pressed bool, x, y int) {
	code, ok := ButtonCodes[button]
	if !ok {
		return
	}
	i.Move(x, y)
	i.notify("NotifyPointerButton", "(ib)", code, pressed)
	if pressed {
		i.pressedButtons[code] = true
	} else {
		delete(i.pressedButtons, code)
	}
}

// Scroll moves the pointer and injects a bounded discrete axis event.
func (i *Input) Scroll(amount, x, y int) {
	steps := ClampSteps(amount)
	if steps == 0 {
		return
	}
	i.Move(x, y)
	i.notify("NotifyPointerAxisDiscrete", "(ui)", uint32(0), steps)
}

// Close releases every held button, keysym, and modifier, swallowing D-Bus
// errors like the Python close().
func (i *Input) Close() {
	if i.closed {
		return
	}
	for code := range i.pressedButtons {
		_ = i.call("NotifyPointerButton", "(ib)", code, false)
	}
	for keysym := range i.pressedKeysyms {
		_ = i.call("NotifyKeyboardKeysym", "(ub)", keysym, false)
	}
	for code := range i.pressedModifiers {
		_ = i.call("NotifyKeyboardKeycode", "(ub)", code, false)
	}
	i.pressedButtons = make(map[int]bool)
	i.pressedKeysyms = make(map[uint32]bool)
	i.pressedModifiers = make(map[int]bool)
	i.closed = true
}
