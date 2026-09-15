package terminal

import (
	"testing"

	"github.com/rarnu/sshdesk-go/internal/input"
	"github.com/rarnu/sshdesk-go/internal/render"
)

func feed(t *testing.T, p *Parser, data []byte, now float64) []input.Event {
	t.Helper()
	events, err := p.Feed(data, now)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func keyEvents(t *testing.T, events []input.Event) []input.KeyEvent {
	t.Helper()
	keys := make([]input.KeyEvent, 0, len(events))
	for _, event := range events {
		key, ok := event.(input.KeyEvent)
		if !ok {
			t.Fatalf("expected KeyEvent, got %#v", event)
		}
		keys = append(keys, key)
	}
	return keys
}

func TestKeyMapping(t *testing.T) {
	parser := &Parser{}
	events := keyEvents(t, feed(t, parser, []byte("aA\r\x7f\t\x1b[A\x1b[24~\x01"), 1.0))
	expected := []input.KeyCode{
		input.KeyCharacter,
		input.KeyCharacter,
		input.KeyEnter,
		input.KeyBackspace,
		input.KeyTab,
		input.KeyUp,
		input.KeyF12,
		input.KeyCharacter,
	}
	if len(events) != len(expected) {
		t.Fatalf("got %d events, want %d", len(events), len(expected))
	}
	for i, code := range expected {
		if events[i].Code != code {
			t.Errorf("event %d: code = %v, want %v", i, events[i].Code, code)
		}
	}
	if events[len(events)-1].Modifiers != input.ModCtrl {
		t.Errorf("last event modifiers = %v, want CTRL", events[len(events)-1].Modifiers)
	}
	if events[1].Modifiers != input.ModShift {
		t.Errorf("uppercase A must add SHIFT, got %v", events[1].Modifiers)
	}
}

func TestEscapeAltAndDetach(t *testing.T) {
	parser := &Parser{}
	alt := keyEvents(t, feed(t, parser, []byte("\x1bx"), 1.0))
	want := input.KeyEvent{Action: 2, Modifiers: input.ModAlt, Code: input.KeyCharacter, Unicode: 'x'}
	if len(alt) != 1 || alt[0] != want {
		t.Fatalf("alt = %#v, want %#v", alt, want)
	}
	events := feed(t, parser, []byte("\x1d\x1d"), 1.0)
	if len(events) != 1 {
		t.Fatalf("detach = %#v", events)
	}
	control, ok := events[0].(input.ControlEvent)
	if !ok || control.Kind != input.ControlExit {
		t.Fatalf("detach = %#v, want EXIT", events[0])
	}

	parser = &Parser{}
	if events := feed(t, parser, []byte("\x1b"), 1.0); len(events) != 0 {
		t.Fatalf("bare ESC must wait, got %#v", events)
	}
	events, err := parser.Flush(1.1)
	if err != nil {
		t.Fatal(err)
	}
	keys := keyEvents(t, events)
	escape := input.KeyEvent{Action: 2, Modifiers: 0, Code: input.KeyEscape, Unicode: 0}
	if len(keys) != 1 || keys[0] != escape {
		t.Fatalf("flush = %#v, want %#v", keys, escape)
	}
}

func TestSingleDetachDegradesToCtrlBracket(t *testing.T) {
	parser := &Parser{}
	events := keyEvents(t, feed(t, parser, []byte("\x1dx"), 1.0))
	want := []input.KeyEvent{
		{Action: 2, Modifiers: input.ModCtrl, Code: input.KeyCharacter, Unicode: ']'},
		{Action: 2, Modifiers: 0, Code: input.KeyCharacter, Unicode: 'x'},
	}
	if len(events) != 2 || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("got %#v, want %#v", events, want)
	}
}

func TestSGRMouse(t *testing.T) {
	parser := &Parser{}
	events := feed(t, parser, []byte("\x1b[<0;10;5M\x1b[<0;10;5m\x1b[<32;11;6M\x1b[<64;11;6M\x1b[<65;11;6M"), 1.0)
	want := []input.Event{
		input.MouseButtonEvent{Button: 1, Pressed: true, Column: 9, Row: 4},
		input.MouseButtonEvent{Button: 1, Pressed: false, Column: 9, Row: 4},
		input.MouseMoveEvent{Column: 10, Row: 5},
		input.MouseScrollEvent{Amount: 1, Column: 10, Row: 5},
		input.MouseScrollEvent{Amount: -1, Column: 10, Row: 5},
	}
	if len(events) != len(want) {
		t.Fatalf("got %#v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event %d: got %#v, want %#v", i, events[i], want[i])
		}
	}
}

func TestConsecutiveMouseMovesAreCoalescedWithoutLosingClicks(t *testing.T) {
	click := input.MouseButtonEvent{Button: 1, Pressed: true, Column: 30, Row: 15}
	events := []input.Event{
		input.MouseMoveEvent{Column: 10, Row: 5},
		input.MouseMoveEvent{Column: 20, Row: 10},
		click,
		input.MouseMoveEvent{Column: 31, Row: 16},
		input.MouseMoveEvent{Column: 32, Row: 17},
	}
	compacted := CoalesceMouseMoves(events)
	want := []input.Event{
		input.MouseMoveEvent{Column: 20, Row: 10},
		click,
		input.MouseMoveEvent{Column: 32, Row: 17},
	}
	if len(compacted) != len(want) {
		t.Fatalf("got %#v", compacted)
	}
	for i := range want {
		if compacted[i] != want[i] {
			t.Errorf("event %d: got %#v, want %#v", i, compacted[i], want[i])
		}
	}
}

func TestLegacyX10MouseFallback(t *testing.T) {
	parser := &Parser{}
	press := append([]byte("\x1b[M"), 32, 42, 37)
	release := append([]byte("\x1b[M"), 35, 42, 37)
	events := feed(t, parser, append(press, release...), 1.0)
	want := []input.Event{
		input.MouseButtonEvent{Button: 1, Pressed: true, Column: 9, Row: 4},
		input.MouseButtonEvent{Button: 1, Pressed: false, Column: 9, Row: 4},
	}
	if len(events) != 2 || events[0] != want[0] || events[1] != want[1] {
		t.Fatalf("got %#v, want %#v", events, want)
	}
}

func TestModifiedNavigationKeys(t *testing.T) {
	parser := &Parser{}
	events := keyEvents(t, feed(t, parser, []byte("\x1b[1;5A\x1b[1;2D\x1b[3;3~\x1b[Z"), 1.0))
	want := []input.KeyEvent{
		{Action: 2, Modifiers: input.ModCtrl, Code: input.KeyUp},
		{Action: 2, Modifiers: input.ModShift, Code: input.KeyLeft},
		{Action: 2, Modifiers: input.ModAlt, Code: input.KeyDelete},
		{Action: 2, Modifiers: input.ModShift, Code: input.KeyTab},
	}
	if len(events) != len(want) {
		t.Fatalf("got %#v", events)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Errorf("event %d: got %#v, want %#v", i, events[i], want[i])
		}
	}
}

func TestCursorReportIsConsumedAsLatencyEvent(t *testing.T) {
	parser := &Parser{}
	events := feed(t, parser, []byte("\x1b[12;40R"), 1.0)
	want := input.TerminalReportEvent{Column: 39, Row: 11}
	if len(events) != 1 || events[0] != want {
		t.Fatalf("got %#v, want %#v", events, want)
	}

	parser = &Parser{}
	if events := feed(t, parser, []byte("\x1b[12;"), 1.0); len(events) != 0 {
		t.Fatalf("partial report must wait, got %#v", events)
	}
	events = feed(t, parser, []byte("40R"), 1.1)
	if len(events) != 1 || events[0] != want {
		t.Fatalf("got %#v, want %#v", events, want)
	}
}

func TestOversizedTerminalSequenceIsRejected(t *testing.T) {
	parser := &Parser{}
	data := append([]byte("\x1b[<"), []byte(make([]byte, 9000))...)
	for i := range data[3:] {
		data[3+i] = '1'
	}
	if _, err := parser.Feed(data, 1.0); err == nil {
		t.Fatal("oversized sequence must fail")
	}
}

func TestUTF8CharactersAndInvalidBytes(t *testing.T) {
	parser := &Parser{}
	events := keyEvents(t, feed(t, parser, []byte("é\xC3"), 1.0))
	if len(events) != 1 || events[0].Unicode != 'é' {
		t.Fatalf("got %#v", events)
	}
	events = keyEvents(t, feed(t, parser, []byte("("), 1.1))
	if len(events) != 2 || events[0].Unicode != '�' || events[1].Unicode != '(' {
		t.Fatalf("split UTF-8 continuation got %#v", events)
	}

	parser = &Parser{}
	events = keyEvents(t, feed(t, parser, []byte{0xFF}, 1.0))
	if len(events) != 1 || events[0].Unicode != '�' {
		t.Fatalf("invalid byte got %#v", events)
	}
}

func TestCoordinateTranslationAndLetterbox(t *testing.T) {
	viewport := render.Viewport{X: 10, Y: 5, Width: 100, Height: 50, DesktopWidth: 1920, DesktopHeight: 1080}
	if _, _, ok := TranslateCoordinates(9, 5, viewport); ok {
		t.Error("left of viewport must be dropped")
	}
	if _, _, ok := TranslateCoordinates(110, 5, viewport); ok {
		t.Error("right of viewport must be dropped")
	}
	x, y, ok := TranslateCoordinates(10, 5, viewport)
	if !ok || x != 9 || y != 10 {
		t.Errorf("got (%d, %d, %v), want (9, 10, true)", x, y, ok)
	}
	x, y, ok = TranslateCoordinates(109, 54, viewport)
	if !ok || x != 1910 || y != 1069 {
		t.Errorf("got (%d, %d, %v), want (1910, 1069, true)", x, y, ok)
	}
}
