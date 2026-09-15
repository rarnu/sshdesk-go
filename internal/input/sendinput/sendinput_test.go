package sendinput

import (
	"testing"

	"github.com/rarnu/sshdesk-go/internal/input"
)

type recordSender struct {
	mice []mouseInput
	keys []keyStroke
}

func (s *recordSender) sendMouse(value mouseInput) error {
	s.mice = append(s.mice, value)
	return nil
}

func (s *recordSender) sendKeyboard(value keyStroke) error {
	s.keys = append(s.keys, value)
	return nil
}

func TestVKKeysTable(t *testing.T) {
	want := map[input.KeyCode]uint16{
		input.KeyBackspace: 0x08, input.KeyTab: 0x09, input.KeyEnter: 0x0D, input.KeyEscape: 0x1B,
		input.KeyPageUp: 0x21, input.KeyPageDown: 0x22, input.KeyEnd: 0x23, input.KeyHome: 0x24,
		input.KeyLeft: 0x25, input.KeyUp: 0x26, input.KeyRight: 0x27, input.KeyDown: 0x28,
		input.KeyInsert: 0x2D, input.KeyDelete: 0x2E,
	}
	for code, vk := range want {
		if vkKeys[code] != vk {
			t.Errorf("vk[%v] = %#x, want %#x", code, vkKeys[code], vk)
		}
	}
	for index := 0; index < 12; index++ {
		code := input.KeyCode(int(input.KeyF1) + index)
		if vkKeys[code] != uint16(0x70+index) {
			t.Errorf("F%d = %#x, want %#x", index+1, vkKeys[code], 0x70+index)
		}
	}
}

func TestModifierVKsOrder(t *testing.T) {
	got := modifierVKs(input.ModCtrl | input.ModAlt | input.ModShift)
	want := []uint16{vkControl, vkAlt, vkShift}
	if len(got) != len(want) {
		t.Fatalf("modifiers = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("modifiers = %v, want %v", got, want)
		}
	}
	if got := modifierVKs(input.ModNone); len(got) != 0 {
		t.Errorf("no modifiers = %v, want empty", got)
	}
}

func TestUTF16UnitsSplitsSurrogatePairs(t *testing.T) {
	if got := utf16Units('a'); len(got) != 1 || got[0] != 0x61 {
		t.Errorf("'a' = %v, want [0x61]", got)
	}
	// U+1F600 encodes as the surrogate pair D83D DE00.
	got := utf16Units(0x1F600)
	if len(got) != 2 || got[0] != 0xD83D || got[1] != 0xDE00 {
		t.Errorf("emoji = %#v, want [0xd83d 0xde00]", got)
	}
}

func TestNormalizeAxis(t *testing.T) {
	if got := normalizeAxis(0, 1920); got != 0 {
		t.Errorf("0 = %d, want 0", got)
	}
	if got := normalizeAxis(1919, 1920); got != 65535 {
		t.Errorf("max = %d, want 65535", got)
	}
	if got := normalizeAxis(-5, 1920); got != 0 {
		t.Errorf("negative = %d, want 0 (clamped)", got)
	}
	if got := normalizeAxis(5000, 1920); got != 65535 {
		t.Errorf("overflow = %d, want 65535 (clamped)", got)
	}
	// Banker's rounding: 0.5 rounds to even.
	if got := normalizeAxis(1, 3); got != 32768 {
		t.Errorf("1/2 of range = %d, want 32768 (round-half-even)", got)
	}
	if got := normalizeAxis(1, 1); got != 0 {
		t.Errorf("single pixel axis = %d, want 0", got)
	}
}

func TestWheelData(t *testing.T) {
	if got := wheelData(3); got != 360 {
		t.Errorf("3 lines = %d, want 360", got)
	}
	if got := wheelData(500); got != 2400 {
		t.Errorf("clamped up = %d, want 2400", got)
	}
	if got := wheelData(-500); int32(got) != -2400 {
		t.Errorf("clamped down = %#x, want two's complement of -2400", got)
	}
}

func TestKeyTapSequenceWithModifiers(t *testing.T) {
	sender := &recordSender{}
	backend := newBackend(sender, 1920, 1080)
	backend.Key(input.KeyEvent{
		Action:    input.KeyTap,
		Modifiers: input.ModCtrl | input.ModAlt,
		Code:      input.KeyEnter,
	})
	want := []keyStroke{
		{vk: vkControl},
		{vk: vkAlt},
		{vk: vkEnter},
		{vk: vkEnter, flags: keyeventfKeyup},
		{vk: vkAlt, flags: keyeventfKeyup},
		{vk: vkControl, flags: keyeventfKeyup},
	}
	if len(sender.keys) != len(want) {
		t.Fatalf("sequence = %+v, want %+v", sender.keys, want)
	}
	for index := range want {
		if sender.keys[index] != want[index] {
			t.Fatalf("sequence = %+v, want %+v", sender.keys, want)
		}
	}
}

func TestKeyUnicodeTapSendsUnitsWithKeyup(t *testing.T) {
	sender := &recordSender{}
	backend := newBackend(sender, 1920, 1080)
	backend.Key(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: 0x1F600})
	want := []keyStroke{
		{scan: 0xD83D, flags: keyeventfUnicode},
		{scan: 0xD83D, flags: keyeventfUnicode | keyeventfKeyup},
		{scan: 0xDE00, flags: keyeventfUnicode},
		{scan: 0xDE00, flags: keyeventfUnicode | keyeventfKeyup},
	}
	if len(sender.keys) != len(want) {
		t.Fatalf("unicode sequence = %+v, want %+v", sender.keys, want)
	}
	for index := range want {
		if sender.keys[index] != want[index] {
			t.Fatalf("unicode sequence = %+v, want %+v", sender.keys, want)
		}
	}
}

func TestKeyPressReleaseTrackingAndUnknownRelease(t *testing.T) {
	sender := &recordSender{}
	backend := newBackend(sender, 1920, 1080)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Modifiers: input.ModCtrl, Code: input.KeyEnter})
	if !backend.pressedKeys[vkControl] || !backend.pressedKeys[vkEnter] {
		t.Error("press must track modifier and key")
	}
	backend.Key(input.KeyEvent{Action: input.KeyRelease, Modifiers: input.ModCtrl, Code: input.KeyEnter})
	if len(backend.pressedKeys) != 0 {
		t.Error("release must clear tracked keys")
	}
	// Unknown keys on tap still release the modifiers.
	sender.keys = nil
	backend.Key(input.KeyEvent{Action: input.KeyTap, Modifiers: input.ModShift, Code: input.KeyCode(999)})
	want := []keyStroke{{vk: vkShift}, {vk: vkShift, flags: keyeventfKeyup}}
	if len(sender.keys) != len(want) || sender.keys[0] != want[0] || sender.keys[1] != want[1] {
		t.Errorf("unknown tap sequence = %+v, want %+v", sender.keys, want)
	}
}

func TestMouseEventsAreAbsoluteVirtualDesk(t *testing.T) {
	sender := &recordSender{}
	backend := newBackend(sender, 1920, 1080)
	backend.Move(1919, 1079)
	backend.Button(1, true, 960, 540)
	backend.Scroll(1, 960, 540)
	if len(sender.mice) != 3 {
		t.Fatalf("mouse records = %d, want 3", len(sender.mice))
	}
	move := sender.mice[0]
	if move.dx != 65535 || move.dy != 65535 {
		t.Errorf("move = (%d, %d), want (65535, 65535)", move.dx, move.dy)
	}
	if move.flags != mouseeventfMove|mouseeventfAbsolute|mouseeventfVirtualDesk {
		t.Errorf("move flags = %#x", move.flags)
	}
	button := sender.mice[1]
	if button.flags != mouseeventfMove|mouseeventfLeftDown|mouseeventfAbsolute|mouseeventfVirtualDesk {
		t.Errorf("button flags = %#x", button.flags)
	}
	scroll := sender.mice[2]
	if scroll.flags != mouseeventfMove|mouseeventfWheel|mouseeventfAbsolute|mouseeventfVirtualDesk {
		t.Errorf("scroll flags = %#x", scroll.flags)
	}
	if scroll.data != 120 {
		t.Errorf("scroll data = %d, want 120", scroll.data)
	}
}

func TestCloseReleasesHeldState(t *testing.T) {
	sender := &recordSender{}
	backend := newBackend(sender, 1920, 1080)
	backend.Button(3, true, 10, 10)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Code: input.KeyF5})
	backend.Close()
	var sawButtonUp, sawKeyUp bool
	for _, event := range sender.mice {
		if event.flags&mouseeventfRightUp != 0 {
			sawButtonUp = true
		}
	}
	for _, event := range sender.keys {
		if event.vk == 0x74 && event.flags == keyeventfKeyup {
			sawKeyUp = true
		}
	}
	if !sawButtonUp || !sawKeyUp {
		t.Errorf("close releases: buttonUp=%v keyUp=%v", sawButtonUp, sawKeyUp)
	}
	if len(backend.pressedButtons) != 0 || len(backend.pressedKeys) != 0 {
		t.Error("close must clear the pressed sets")
	}
}
