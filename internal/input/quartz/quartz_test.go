package quartz

import (
	"testing"

	"github.com/rylena/sshdesk-go/internal/input"
)

type keyRecord struct {
	code  uint16
	down  bool
	text  string
	flags uint64
}

type mouseRecord struct {
	kind   int32
	x, y   float64
	button int32
}

type recordPoster struct {
	keys    []keyRecord
	mice    []mouseRecord
	scrolls []int32
}

func (p *recordPoster) postKeyboardEvent(code uint16, down bool, text string, flags uint64) {
	p.keys = append(p.keys, keyRecord{code, down, text, flags})
}

func (p *recordPoster) postMouseEvent(kind int32, x, y float64, button int32) {
	p.mice = append(p.mice, mouseRecord{kind, x, y, button})
}

func (p *recordPoster) postScrollEvent(lines int32) {
	p.scrolls = append(p.scrolls, lines)
}

func TestMacKeycodesTable(t *testing.T) {
	want := map[input.KeyCode]uint16{
		input.KeyEnter: 36, input.KeyTab: 48, input.KeyBackspace: 51, input.KeyEscape: 53,
		input.KeyHome: 115, input.KeyEnd: 119, input.KeyPageUp: 116, input.KeyPageDown: 121,
		input.KeyDelete: 117, input.KeyLeft: 123, input.KeyRight: 124,
		input.KeyDown: 125, input.KeyUp: 126,
		input.KeyF1: 122, input.KeyF2: 120, input.KeyF3: 99, input.KeyF4: 118,
		input.KeyF5: 96, input.KeyF6: 97, input.KeyF7: 98, input.KeyF8: 100,
		input.KeyF9: 101, input.KeyF10: 109, input.KeyF11: 103, input.KeyF12: 111,
	}
	if len(macKeycodes) != len(want) {
		t.Fatalf("keycode table = %d entries, want %d", len(macKeycodes), len(want))
	}
	for code, vk := range want {
		if macKeycodes[code] != vk {
			t.Errorf("keycode[%v] = %d, want %d", code, macKeycodes[code], vk)
		}
	}
}

func TestFlagsForModifiers(t *testing.T) {
	if got := flagsForModifiers(input.ModNone); got != 0 {
		t.Errorf("none = %#x, want 0", got)
	}
	if got := flagsForModifiers(input.ModCtrl); got != flagMaskControl {
		t.Errorf("ctrl = %#x, want %#x", got, flagMaskControl)
	}
	if got := flagsForModifiers(input.ModAlt); got != flagMaskAlternate {
		t.Errorf("alt = %#x, want %#x", got, flagMaskAlternate)
	}
	if got := flagsForModifiers(input.ModShift); got != flagMaskShift {
		t.Errorf("shift = %#x, want %#x", got, flagMaskShift)
	}
	want := uint64(flagMaskControl | flagMaskAlternate | flagMaskShift)
	if got := flagsForModifiers(input.ModCtrl | input.ModAlt | input.ModShift); got != want {
		t.Errorf("all = %#x, want %#x", got, want)
	}
}

func TestKeyTapPostsDownThenUpWithUnicodeAndFlags(t *testing.T) {
	poster := &recordPoster{}
	backend := newBackend(poster, 1920, 1080)
	backend.Key(input.KeyEvent{
		Action:    input.KeyTap,
		Modifiers: input.ModCtrl | input.ModShift,
		Code:      input.KeyCharacter,
		Unicode:   'a',
	})
	if len(poster.keys) != 2 {
		t.Fatalf("tap events = %d, want 2", len(poster.keys))
	}
	wantFlags := uint64(flagMaskControl | flagMaskShift)
	for index, down := range []bool{true, false} {
		event := poster.keys[index]
		if event.down != down || event.text != "a" || event.flags != wantFlags {
			t.Errorf("event %d = %+v, want down=%v text=a flags=%#x", index, event, down, wantFlags)
		}
	}
}

func TestKeySpecialCodeAndUnknownRejection(t *testing.T) {
	poster := &recordPoster{}
	backend := newBackend(poster, 1920, 1080)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Code: input.KeyEnter})
	backend.Key(input.KeyEvent{Action: input.KeyRelease, Code: input.KeyEnter})
	if len(poster.keys) != 2 || poster.keys[0].code != 36 || !poster.keys[0].down || poster.keys[1].down {
		t.Fatalf("enter press/release = %+v", poster.keys)
	}
	backend.Key(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCode(999)})
	if len(poster.keys) != 2 {
		t.Error("unknown key codes must be ignored")
	}
	backend.Key(input.KeyEvent{Action: input.KeyTap, Code: input.KeyEnter})
	if len(poster.keys) != 4 {
		t.Error("known key tap must post two events")
	}
}

func TestMoveSwitchesToDragKindWhileButtonHeld(t *testing.T) {
	poster := &recordPoster{}
	backend := newBackend(poster, 1920, 1080)
	backend.Move(10, 10)
	backend.Button(1, true, 10, 10)
	backend.Move(20, 20)
	backend.Button(1, false, 20, 20)
	backend.Button(3, true, 20, 20)
	backend.Move(30, 30)
	if len(poster.mice) != 6 {
		t.Fatalf("mouse events = %d, want 6", len(poster.mice))
	}
	kinds := make([]int32, len(poster.mice))
	for index, event := range poster.mice {
		kinds[index] = event.kind
	}
	want := []int32{mouseMoved, leftMouseDown, leftMouseDragged, leftMouseUp, rightMouseDown, rightMouseDragged}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	if poster.mice[1].button != 0 || poster.mice[4].button != 1 {
		t.Errorf("CGMouseButton mapping wrong: left=%d right=%d, want 0 and 1",
			poster.mice[1].button, poster.mice[4].button)
	}
}

func TestButtonMiddleAndInvalidRejection(t *testing.T) {
	poster := &recordPoster{}
	backend := newBackend(poster, 1920, 1080)
	backend.Button(2, true, 5, 5)
	backend.Button(7, true, 5, 5)
	if len(poster.mice) != 1 {
		t.Fatalf("events = %d, want 1 (button 7 ignored)", len(poster.mice))
	}
	if poster.mice[0].kind != otherMouseDown || poster.mice[0].button != 2 {
		t.Errorf("middle button = %+v, want otherMouseDown button 2", poster.mice[0])
	}
}

func TestPointClampsToDisplayBounds(t *testing.T) {
	poster := &recordPoster{}
	backend := newBackend(poster, 1920, 1080)
	backend.Move(-50, 5000)
	last := poster.mice[len(poster.mice)-1]
	if last.x != 0 || last.y != 1079 {
		t.Errorf("clamped point = (%v, %v), want (0, 1079)", last.x, last.y)
	}
}

func TestScrollMovesAndClampsLines(t *testing.T) {
	poster := &recordPoster{}
	backend := newBackend(poster, 1920, 1080)
	backend.Scroll(500, 100, 100)
	backend.Scroll(-500, 100, 100)
	if len(poster.mice) != 2 {
		t.Errorf("scroll must move the pointer first (2 moves), got %d", len(poster.mice))
	}
	if len(poster.scrolls) != 2 || poster.scrolls[0] != 20 || poster.scrolls[1] != -20 {
		t.Errorf("scroll lines = %v, want [20 -20]", poster.scrolls)
	}
}

func TestCloseReleasesHeldButtonsAndKeys(t *testing.T) {
	poster := &recordPoster{}
	backend := newBackend(poster, 1920, 1080)
	backend.Button(1, true, 10, 10)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Code: input.KeyEnter})
	backend.Close()
	var sawButtonUp, sawKeyUp bool
	for _, event := range poster.mice {
		if event.kind == leftMouseUp {
			sawButtonUp = true
		}
	}
	for _, event := range poster.keys {
		if !event.down && event.code == 36 {
			sawKeyUp = true
		}
	}
	if !sawButtonUp || !sawKeyUp {
		t.Errorf("close must release held state: buttonUp=%v keyUp=%v", sawButtonUp, sawKeyUp)
	}
	if len(backend.pressedButtons) != 0 || len(backend.pressedKeys) != 0 {
		t.Error("close must clear the pressed sets")
	}
}
