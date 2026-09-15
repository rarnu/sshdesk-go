package x11

import (
	"reflect"
	"testing"

	"github.com/rarnu/sshdesk-go/internal/input"
)

func TestSpecialKeysTable(t *testing.T) {
	if len(SpecialKeys) != 26 {
		t.Fatalf("SpecialKeys has %d entries, want 26", len(SpecialKeys))
	}
	spot := map[input.KeyCode]SpecialKey{
		input.KeyEnter:     {"Return", 0xFF0D},
		input.KeyEscape:    {"Escape", 0xFF1B},
		input.KeyBackspace: {"BackSpace", 0xFF08},
		input.KeyTab:       {"Tab", 0xFF09},
		input.KeyPageUp:    {"Prior", 0xFF55},
		input.KeyPageDown:  {"Next", 0xFF56},
		input.KeyDelete:    {"Delete", 0xFFFF},
		input.KeyF1:        {"F1", 0xFFBE},
		input.KeyF12:       {"F12", 0xFFC9},
	}
	for code, want := range spot {
		if got := SpecialKeys[code]; got != want {
			t.Fatalf("SpecialKeys[%d] = %+v, want %+v", code, got, want)
		}
	}
	for index := input.KeyF1; index <= input.KeyF12; index++ {
		if _, ok := SpecialKeys[index]; !ok {
			t.Fatalf("SpecialKeys is missing F%d", index-input.KeyF1+1)
		}
	}
}

func TestScanCharacterKeycode(t *testing.T) {
	lookup := func(keycode, level int) uint32 {
		switch {
		case keycode == 40 && level == 0:
			return 'a'
		case keycode == 41 && level == 1:
			return 'A'
		case keycode == 42 && level == 3:
			return 'x'
		case keycode == 30 && level == 2:
			return 'q'
		}
		return 0
	}

	keycode, shift := ScanCharacterKeycode('a', 8, 100, lookup)
	if keycode != 40 || shift {
		t.Fatalf("unshifted scan = (%d, %v), want (40, false)", keycode, shift)
	}
	keycode, shift = ScanCharacterKeycode('A', 8, 100, lookup)
	if keycode != 41 || !shift {
		t.Fatalf("level-1 scan = (%d, %v), want (41, true)", keycode, shift)
	}
	keycode, shift = ScanCharacterKeycode('x', 8, 100, lookup)
	if keycode != 42 || !shift {
		t.Fatalf("level-3 scan = (%d, %v), want (42, true)", keycode, shift)
	}
	keycode, shift = ScanCharacterKeycode('q', 8, 100, lookup)
	if keycode != 30 || shift {
		t.Fatalf("level-2 scan = (%d, %v), want (30, false)", keycode, shift)
	}
	keycode, shift = ScanCharacterKeycode('z', 8, 100, lookup)
	if keycode != 0 || shift {
		t.Fatalf("missing scan = (%d, %v), want (0, false)", keycode, shift)
	}
}

func TestKeysymToKeycodePrefersLowestLevelThenKeycode(t *testing.T) {
	lookup := func(keycode, level int) uint32 {
		switch {
		case keycode == 60 && level == 1:
			return 0xBEEF
		case keycode == 50 && level == 2:
			return 0xBEEF
		case keycode == 40 && level == 0:
			return 0xCAFE
		case keycode == 30 && level == 0:
			return 0xCAFE
		}
		return 0
	}
	if got := KeysymToKeycode(0xBEEF, 8, 100, 4, lookup); got != 60 {
		t.Fatalf("KeysymToKeycode level preference = %d, want 60", got)
	}
	if got := KeysymToKeycode(0xCAFE, 8, 100, 4, lookup); got != 30 {
		t.Fatalf("KeysymToKeycode keycode preference = %d, want 30", got)
	}
	if got := KeysymToKeycode(0xDEAD, 8, 100, 4, lookup); got != 0 {
		t.Fatalf("KeysymToKeycode miss = %d, want 0", got)
	}
}

func TestModifierKeysymsOrder(t *testing.T) {
	if got := ModifierKeysyms(input.ModCtrl | input.ModAlt); !reflect.DeepEqual(got, []uint32{KeysymControlL, KeysymAltL}) {
		t.Fatalf("ctrl+alt = %v", got)
	}
	if got := ModifierKeysyms(input.ModShift); !reflect.DeepEqual(got, []uint32{KeysymShiftL}) {
		t.Fatalf("shift = %v", got)
	}
	if got := ModifierKeysyms(input.ModCtrl | input.ModAlt | input.ModShift); !reflect.DeepEqual(got, []uint32{KeysymControlL, KeysymAltL, KeysymShiftL}) {
		t.Fatalf("ctrl+alt+shift = %v", got)
	}
	if got := ModifierKeysyms(input.ModNone); len(got) != 0 {
		t.Fatalf("none = %v", got)
	}
}

func TestKeySequence(t *testing.T) {
	modifiers := []int{10, 20}
	wantTap := []KeyAction{
		{Press: true, Keycode: 10},
		{Press: true, Keycode: 20},
		{Press: true, Keycode: 30},
		{Press: false, Keycode: 30},
		{Press: false, Keycode: 20},
		{Press: false, Keycode: 10},
	}
	if got := KeySequence(input.KeyTap, modifiers, 30); !reflect.DeepEqual(got, wantTap) {
		t.Fatalf("tap sequence = %v, want %v", got, wantTap)
	}
	if got := KeySequence(input.KeyPress, modifiers, 30); !reflect.DeepEqual(got, wantTap[:3]) {
		t.Fatalf("press sequence = %v, want %v", got, wantTap[:3])
	}
	if got := KeySequence(input.KeyRelease, modifiers, 30); !reflect.DeepEqual(got, wantTap[3:]) {
		t.Fatalf("release sequence = %v, want %v", got, wantTap[3:])
	}
}

func TestScrollButton(t *testing.T) {
	cases := []struct {
		amount  int
		button  int
		repeats int
	}{
		{99, 4, 20},
		{7, 4, 7},
		{-3, 5, 3},
		{-99, 5, 20},
		{0, 5, 0},
	}
	for _, tc := range cases {
		button, repeats := ScrollButton(tc.amount)
		if button != tc.button || repeats != tc.repeats {
			t.Fatalf("ScrollButton(%d) = (%d, %d), want (%d, %d)", tc.amount, button, repeats, tc.button, tc.repeats)
		}
	}
}

func TestClampCoords(t *testing.T) {
	if x, y := ClampCoords(-5, 9999, 100, 50); x != 0 || y != 49 {
		t.Fatalf("ClampCoords(-5, 9999) = (%d, %d), want (0, 49)", x, y)
	}
	if x, y := ClampCoords(30, 20, 100, 50); x != 30 || y != 20 {
		t.Fatalf("ClampCoords(30, 20) = (%d, %d), want (30, 20)", x, y)
	}
}

func TestCharacterKeysym(t *testing.T) {
	if got := CharacterKeysym('a'); got != 0x61 {
		t.Fatalf("CharacterKeysym('a') = %#x, want 0x61", got)
	}
	if got := CharacterKeysym('é'); got != 0xE9 {
		t.Fatalf("CharacterKeysym('é') = %#x, want 0xe9", got)
	}
}
