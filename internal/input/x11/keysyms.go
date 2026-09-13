// Package x11 injects keyboard and pointer events through the XTEST
// extension. The keysym tables and event-planning logic are platform-neutral
// so they can be tested without an X server; the X wiring is Linux-only.
package x11

import "github.com/rylena/sshdesk-go/internal/input"

// Keysym values from X11 keysymdef.h.
const (
	KeysymBackSpace = 0xFF08
	KeysymTab       = 0xFF09
	KeysymReturn    = 0xFF0D
	KeysymEscape    = 0xFF1B
	KeysymHome      = 0xFF50
	KeysymLeft      = 0xFF51
	KeysymUp        = 0xFF52
	KeysymRight     = 0xFF53
	KeysymDown      = 0xFF54
	KeysymPrior     = 0xFF55 // Page Up
	KeysymNext      = 0xFF56 // Page Down
	KeysymEnd       = 0xFF57
	KeysymInsert    = 0xFF63
	KeysymDelete    = 0xFFFF
	KeysymF1        = 0xFFBE
	KeysymF12       = 0xFFC9
	KeysymShiftL    = 0xFFE1
	KeysymControlL  = 0xFFE3
	KeysymAltL      = 0xFFE9
)

// SpecialKey is one named non-character key.
type SpecialKey struct {
	Name   string
	Keysym uint32
}

// SpecialKeys mirrors the Python SPECIAL_KEYSYMS table.
var SpecialKeys = map[input.KeyCode]SpecialKey{
	input.KeyEnter:     {"Return", KeysymReturn},
	input.KeyEscape:    {"Escape", KeysymEscape},
	input.KeyBackspace: {"BackSpace", KeysymBackSpace},
	input.KeyTab:       {"Tab", KeysymTab},
	input.KeyUp:        {"Up", KeysymUp},
	input.KeyDown:      {"Down", KeysymDown},
	input.KeyRight:     {"Right", KeysymRight},
	input.KeyLeft:      {"Left", KeysymLeft},
	input.KeyHome:      {"Home", KeysymHome},
	input.KeyEnd:       {"End", KeysymEnd},
	input.KeyPageUp:    {"Prior", KeysymPrior},
	input.KeyPageDown:  {"Next", KeysymNext},
	input.KeyInsert:    {"Insert", KeysymInsert},
	input.KeyDelete:    {"Delete", KeysymDelete},
	input.KeyF1:        {"F1", KeysymF1},
	input.KeyF2:        {"F2", KeysymF1 + 1},
	input.KeyF3:        {"F3", KeysymF1 + 2},
	input.KeyF4:        {"F4", KeysymF1 + 3},
	input.KeyF5:        {"F5", KeysymF1 + 4},
	input.KeyF6:        {"F6", KeysymF1 + 5},
	input.KeyF7:        {"F7", KeysymF1 + 6},
	input.KeyF8:        {"F8", KeysymF1 + 7},
	input.KeyF9:        {"F9", KeysymF1 + 8},
	input.KeyF10:       {"F10", KeysymF1 + 9},
	input.KeyF11:       {"F11", KeysymF1 + 10},
	input.KeyF12:       {"F12", KeysymF12},
}

// ModifierKeysyms returns the modifier keysyms in the Python press order
// (Control_L, Alt_L, Shift_L).
func ModifierKeysyms(modifiers input.Modifiers) []uint32 {
	var keysyms []uint32
	if modifiers&input.ModCtrl != 0 {
		keysyms = append(keysyms, KeysymControlL)
	}
	if modifiers&input.ModAlt != 0 {
		keysyms = append(keysyms, KeysymAltL)
	}
	if modifiers&input.ModShift != 0 {
		keysyms = append(keysyms, KeysymShiftL)
	}
	return keysyms
}

// CharacterKeysym resolves the keysym scanned for a Unicode character,
// mirroring XK.string_to_keysym with the ord() fallback: Latin-1 characters
// map to themselves and everything else keeps its code point (which simply
// never matches the keyboard scan).
func CharacterKeysym(character rune) uint32 {
	return uint32(character)
}

// ScanCharacterKeycode mirrors X11Input._character_keycode: keycodes are
// scanned outer, levels 0..3 inner, and the first keycode whose keysym
// matches wins. Levels 1 and 3 require an extra Shift press.
func ScanCharacterKeycode(keysym uint32, minKeycode, maxKeycode int, lookup func(keycode, level int) uint32) (keycode int, extraShift bool) {
	for code := minKeycode; code <= maxKeycode; code++ {
		for level := 0; level < 4; level++ {
			if lookup(code, level) == keysym {
				return code, level == 1 || level == 3
			}
		}
	}
	return 0, false
}

// KeysymToKeycode mirrors python-xlib's Display.keysym_to_keycode: among all
// bindings the one with the lowest level and then the lowest keycode wins.
func KeysymToKeycode(keysym uint32, minKeycode, maxKeycode, levels int, lookup func(keycode, level int) uint32) int {
	for level := 0; level < levels; level++ {
		for code := minKeycode; code <= maxKeycode; code++ {
			if lookup(code, level) == keysym {
				return code
			}
		}
	}
	return 0
}

// KeyAction is one fake key press or release.
type KeyAction struct {
	Press   bool
	Keycode int
}

// KeySequence builds the ordered fake-input list for one key event,
// mirroring X11Input.key: modifiers press in order, the main key presses,
// the main key releases, then the modifiers release in reverse.
func KeySequence(action int, modifierCodes []int, keycode int) []KeyAction {
	var sequence []KeyAction
	if action == input.KeyPress || action == input.KeyTap {
		for _, modifier := range modifierCodes {
			sequence = append(sequence, KeyAction{Press: true, Keycode: modifier})
		}
		sequence = append(sequence, KeyAction{Press: true, Keycode: keycode})
	}
	if action == input.KeyRelease || action == input.KeyTap {
		sequence = append(sequence, KeyAction{Press: false, Keycode: keycode})
		for index := len(modifierCodes) - 1; index >= 0; index-- {
			sequence = append(sequence, KeyAction{Press: false, Keycode: modifierCodes[index]})
		}
	}
	return sequence
}

// ScrollButton maps a scroll amount to the wheel button (4 up, 5 down) and
// the bounded repetition count.
func ScrollButton(amount int) (button int, repeats int) {
	button = 5
	if amount > 0 {
		button = 4
	}
	repeats = amount
	if repeats < 0 {
		repeats = -repeats
	}
	if repeats > 20 {
		repeats = 20
	}
	return button, repeats
}

// ClampCoords bounds a pointer position to the desktop.
func ClampCoords(x, y, width, height int) (int, int) {
	return min(max(0, x), width-1), min(max(0, y), height-1)
}
