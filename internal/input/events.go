// Package input defines the normalized desktop input event model and the
// input backend interface.
package input

type Modifiers int

const (
	ModNone  Modifiers = 0
	ModShift Modifiers = 1
	ModAlt   Modifiers = 2
	ModCtrl  Modifiers = 4
)

type KeyCode int

const (
	KeyCharacter KeyCode = 0
	KeyEnter     KeyCode = 1
	KeyEscape    KeyCode = 2
	KeyBackspace KeyCode = 3
	KeyTab       KeyCode = 4
	KeyUp        KeyCode = 5
	KeyDown      KeyCode = 6
	KeyRight     KeyCode = 7
	KeyLeft      KeyCode = 8
	KeyHome      KeyCode = 9
	KeyEnd       KeyCode = 10
	KeyPageUp    KeyCode = 11
	KeyPageDown  KeyCode = 12
	KeyInsert    KeyCode = 13
	KeyDelete    KeyCode = 14
	KeyF1        KeyCode = 20
	KeyF2        KeyCode = 21
	KeyF3        KeyCode = 22
	KeyF4        KeyCode = 23
	KeyF5        KeyCode = 24
	KeyF6        KeyCode = 25
	KeyF7        KeyCode = 26
	KeyF8        KeyCode = 27
	KeyF9        KeyCode = 28
	KeyF10       KeyCode = 29
	KeyF11       KeyCode = 30
	KeyF12       KeyCode = 31
)

// Key actions.
const (
	KeyPress   = 0
	KeyRelease = 1
	KeyTap     = 2
)

type ControlKind int

const (
	ControlExit ControlKind = iota
	ControlToggleStats
)

// KeyEvent is a normalized key action independent of terminal and desktop
// backends.
type KeyEvent struct {
	Action    int
	Modifiers Modifiers
	Code      KeyCode
	Unicode   rune
}

type ControlEvent struct {
	Kind ControlKind
}

type MouseMoveEvent struct {
	Column int
	Row    int
}

type MouseButtonEvent struct {
	Button  int
	Pressed bool
	Column  int
	Row     int
}

type MouseScrollEvent struct {
	Amount int
	Column int
	Row    int
}

// TerminalReportEvent is the response to an ANSI cursor-position query used
// for terminal RTT measurement.
type TerminalReportEvent struct {
	Column int
	Row    int
}

// Event is any terminal input event.
type Event interface{ terminalInputEvent() }

func (KeyEvent) terminalInputEvent()            {}
func (ControlEvent) terminalInputEvent()        {}
func (MouseMoveEvent) terminalInputEvent()      {}
func (MouseButtonEvent) terminalInputEvent()    {}
func (MouseScrollEvent) terminalInputEvent()    {}
func (TerminalReportEvent) terminalInputEvent() {}

// Backend injects input events into the remote desktop.
type Backend interface {
	Key(event KeyEvent)
	Move(x, y int)
	Button(button int, pressed bool, x, y int)
	Scroll(amount, x, y int)
	Close()
}

// NullBackend discards every event.
type NullBackend struct{}

func (NullBackend) Key(KeyEvent)               {}
func (NullBackend) Move(int, int)              {}
func (NullBackend) Button(int, bool, int, int) {}
func (NullBackend) Scroll(int, int, int)       {}
func (NullBackend) Close()                     {}
