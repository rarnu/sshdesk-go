package mutter

import (
	"errors"
	"reflect"
	"testing"

	"github.com/rarnu/sshdesk-go/internal/input"
)

// recordedCall is one captured D-Bus call.
type recordedCall struct {
	method    string
	signature string
	values    []any
}

// recordingInput builds an Input with a scripted caller.
func recordingInput(calls *[]recordedCall) *Input {
	return New(
		func(method, signature string, values ...any) error {
			*calls = append(*calls, recordedCall{method, signature, values})
			return nil
		},
		"/remote/session",
		"/screen/stream",
		func() (int, int) { return 1920, 1080 },
		nil,
	)
}

func TestSpecialKeysymsTable(t *testing.T) {
	if len(SpecialKeysyms) != 26 {
		t.Fatalf("SpecialKeysyms has %d entries, want 26", len(SpecialKeysyms))
	}
	spot := map[input.KeyCode]uint32{
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
		input.KeyF11:       0xFFC8,
		input.KeyF12:       0xFFC9,
	}
	for code, want := range spot {
		if got := SpecialKeysyms[code]; got != want {
			t.Fatalf("SpecialKeysyms[%d] = %#x, want %#x", code, got, want)
		}
	}
}

func TestKeysymResolution(t *testing.T) {
	keysym, ok := Keysym(input.KeyEvent{Code: input.KeyCharacter, Unicode: 'c'})
	if !ok || keysym != 0x63 {
		t.Fatalf("'c' = (%#x, %v)", keysym, ok)
	}
	keysym, ok = Keysym(input.KeyEvent{Code: input.KeyCharacter, Unicode: 'é'})
	if !ok || keysym != 0xE9 {
		t.Fatalf("'é' = (%#x, %v)", keysym, ok)
	}
	keysym, ok = Keysym(input.KeyEvent{Code: input.KeyCharacter, Unicode: '中'})
	if !ok || keysym != 0x01004E2D {
		t.Fatalf("'中' = (%#x, %v)", keysym, ok)
	}
	if _, ok := Keysym(input.KeyEvent{Code: input.KeyCharacter, Unicode: 0}); ok {
		t.Fatal("unicode 0 resolved")
	}
	if _, ok := Keysym(input.KeyEvent{Code: input.KeyCharacter, Unicode: 0x110000}); ok {
		t.Fatal("unicode >0x10FFFF resolved")
	}
	keysym, ok = Keysym(input.KeyEvent{Code: input.KeyEnter})
	if !ok || keysym != 0xFF0D {
		t.Fatalf("enter = (%#x, %v)", keysym, ok)
	}
	if _, ok := Keysym(input.KeyEvent{Code: input.KeyCode(15)}); ok {
		t.Fatal("unknown key code resolved")
	}
}

func TestModifierCodesOrder(t *testing.T) {
	if got := ModifierCodes(input.ModCtrl | input.ModShift | input.ModAlt); !reflect.DeepEqual(got, []int{29, 42, 56}) {
		t.Fatalf("ctrl+shift+alt = %v, want [29 42 56]", got)
	}
	if got := ModifierCodes(input.ModNone); len(got) != 0 {
		t.Fatalf("none = %v", got)
	}
}

func TestButtonCodes(t *testing.T) {
	if ButtonCodes[1] != 0x110 || ButtonCodes[2] != 0x112 || ButtonCodes[3] != 0x111 {
		t.Fatalf("ButtonCodes = %v", ButtonCodes)
	}
}

func TestBoundedPoint(t *testing.T) {
	x, y := BoundedPoint(9999, -50, 1920, 1080)
	if x != 1919.0 || y != 0.0 {
		t.Fatalf("BoundedPoint(9999, -50) = (%v, %v), want (1919, 0)", x, y)
	}
	x, y = BoundedPoint(100, 200, 1920, 1080)
	if x != 100.0 || y != 200.0 {
		t.Fatalf("BoundedPoint(100, 200) = (%v, %v)", x, y)
	}
}

func TestClampSteps(t *testing.T) {
	if ClampSteps(99) != 20 || ClampSteps(-99) != -20 || ClampSteps(7) != 7 || ClampSteps(0) != 0 {
		t.Fatal("ClampSteps bounds are wrong")
	}
}

func TestMoveUsesLinkedStreamAndBoundsPointer(t *testing.T) {
	var calls []recordedCall
	backend := recordingInput(&calls)
	backend.Move(9999, -50)
	want := recordedCall{"NotifyPointerMotionAbsolute", "(sdd)", []any{"/screen/stream", 1919.0, 0.0}}
	if len(calls) != 1 || !reflect.DeepEqual(calls[0], want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
}

func TestMoveReportsCursorToCapture(t *testing.T) {
	var x, y int
	backend := New(
		func(method, signature string, values ...any) error { return nil },
		"/remote/session", "/screen/stream",
		func() (int, int) { return 1920, 1080 },
		func(cx, cy int) { x, y = cx, cy },
	)
	backend.Move(9999, -50)
	if x != 1919 || y != 0 {
		t.Fatalf("cursor = (%d, %d), want (1919, 0)", x, y)
	}
}

func TestKeyTapSequenceWithModifier(t *testing.T) {
	var calls []recordedCall
	backend := recordingInput(&calls)
	backend.Key(input.KeyEvent{Action: input.KeyTap, Modifiers: input.ModCtrl, Code: input.KeyCharacter, Unicode: 'c'})
	want := []recordedCall{
		{"NotifyKeyboardKeycode", "(ub)", []any{29, true}},
		{"NotifyKeyboardKeysym", "(ub)", []any{uint32('c'), true}},
		{"NotifyKeyboardKeysym", "(ub)", []any{uint32('c'), false}},
		{"NotifyKeyboardKeycode", "(ub)", []any{29, false}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
	if len(backend.pressedKeysyms) != 0 || len(backend.pressedModifiers) != 0 {
		t.Fatal("tap left pressed state behind")
	}
}

func TestKeyPressTracksAndReleaseDiscards(t *testing.T) {
	var calls []recordedCall
	backend := recordingInput(&calls)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Code: input.KeyEnter})
	if !backend.pressedKeysyms[0xFF0D] {
		t.Fatal("enter not tracked")
	}
	backend.Key(input.KeyEvent{Action: input.KeyRelease, Code: input.KeyEnter})
	if len(backend.pressedKeysyms) != 0 {
		t.Fatal("enter not released")
	}
	want := []recordedCall{
		{"NotifyKeyboardKeysym", "(ub)", []any{uint32(0xFF0D), true}},
		{"NotifyKeyboardKeysym", "(ub)", []any{uint32(0xFF0D), false}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
}

func TestKeyIgnoresUnsupportedEvents(t *testing.T) {
	var calls []recordedCall
	backend := recordingInput(&calls)
	backend.Key(input.KeyEvent{Action: 9, Code: input.KeyEnter})
	backend.Key(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: 0})
	backend.Key(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCode(15)})
	if len(calls) != 0 {
		t.Fatalf("unsupported events spawned calls: %+v", calls)
	}
}

func TestButtonMovesClicksAndTracks(t *testing.T) {
	var calls []recordedCall
	backend := recordingInput(&calls)
	backend.Button(1, true, 20, 30)
	backend.Button(1, false, 20, 30)
	want := []recordedCall{
		{"NotifyPointerMotionAbsolute", "(sdd)", []any{"/screen/stream", 20.0, 30.0}},
		{"NotifyPointerButton", "(ib)", []any{0x110, true}},
		{"NotifyPointerMotionAbsolute", "(sdd)", []any{"/screen/stream", 20.0, 30.0}},
		{"NotifyPointerButton", "(ib)", []any{0x110, false}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
	if len(backend.pressedButtons) != 0 {
		t.Fatal("button left pressed state behind")
	}
	backend.Button(9, true, 0, 0)
	if len(calls) != 4 {
		t.Fatalf("unsupported button spawned calls: %+v", calls)
	}
}

func TestScrollClampsAndSkipsZero(t *testing.T) {
	var calls []recordedCall
	backend := recordingInput(&calls)
	backend.Scroll(0, 0, 0)
	if len(calls) != 0 {
		t.Fatalf("zero scroll spawned calls: %+v", calls)
	}
	backend.Scroll(99, 10, 20)
	want := []recordedCall{
		{"NotifyPointerMotionAbsolute", "(sdd)", []any{"/screen/stream", 10.0, 20.0}},
		{"NotifyPointerAxisDiscrete", "(ui)", []any{uint32(0), 20}},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %+v, want %+v", calls, want)
	}
}

func TestCloseReleasesHeldStateAndBlocksFurtherCalls(t *testing.T) {
	var calls []recordedCall
	backend := recordingInput(&calls)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Modifiers: input.ModCtrl, Code: input.KeyEnter})
	backend.Button(2, true, 0, 0)
	calls = nil
	backend.Close()

	assertContains := func(method string, values ...any) {
		t.Helper()
		for _, call := range calls {
			if call.method == method && reflect.DeepEqual(call.values, values) {
				return
			}
		}
		t.Fatalf("calls = %+v, missing %s %v", calls, method, values)
	}
	assertContains("NotifyPointerButton", 0x112, false)
	assertContains("NotifyKeyboardKeysym", uint32(0xFF0D), false)
	assertContains("NotifyKeyboardKeycode", 29, false)
	if len(backend.pressedButtons) != 0 || len(backend.pressedKeysyms) != 0 || len(backend.pressedModifiers) != 0 {
		t.Fatal("Close() did not clear the pressed sets")
	}

	calls = nil
	backend.Move(1, 1)
	backend.Key(input.KeyEvent{Action: input.KeyTap, Code: input.KeyEnter})
	if len(calls) != 0 {
		t.Fatalf("closed backend still calls: %+v", calls)
	}
}

func TestCloseSwallowsDBusErrors(t *testing.T) {
	backend := New(
		func(method, signature string, values ...any) error { return errors.New("GNOME input failed: boom") },
		"/remote/session", "/screen/stream",
		func() (int, int) { return 100, 100 },
		nil,
	)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Code: input.KeyEnter})
	backend.Close()
	if len(backend.pressedKeysyms) != 0 {
		t.Fatal("Close() did not clear the pressed sets")
	}
}
