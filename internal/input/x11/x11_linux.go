//go:build linux

package x11

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"

	"github.com/rarnu/sshdesk-go/internal/input"
)

const (
	eventKeyPress      = 2
	eventKeyRelease    = 3
	eventButtonPress   = 4
	eventButtonRelease = 5
	eventMotionNotify  = 6
)

// characterMapping is the cached scan result for one Unicode character.
type characterMapping struct {
	keycode    int
	extraShift bool
}

// Input injects bounded keyboard and pointer events through XTEST, mirroring
// the Python X11Input.
type Input struct {
	mu     sync.Mutex
	conn   *xgb.Conn
	root   xproto.Window
	width  int
	height int

	minKeycode        int
	maxKeycode        int
	keysymsPerKeycode int
	keysyms           []uint32

	pressedKeys    map[int]bool
	pressedButtons map[int]bool
	modifierCache  map[input.Modifiers][]int
	characterCache map[rune]characterMapping
}

var _ input.Backend = (*Input)(nil)

// NewInput opens the display and verifies the XTEST extension.
func NewInput(displayName string) (*Input, error) {
	if displayName == "" {
		displayName = os.Getenv("DISPLAY")
	}
	if displayName == "" {
		return nil, errors.New("DISPLAY is not set; X11 input is unavailable")
	}
	conn, err := xgb.NewConnDisplay(displayName)
	if err != nil {
		return nil, fmt.Errorf("cannot open X11 display %q", displayName)
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	geometry, err := xproto.GetGeometry(conn, xproto.Drawable(screen.Root)).Reply()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot query the X11 root geometry: %v", err)
	}
	extension, err := xproto.QueryExtension(conn, uint16(len("XTEST")), "XTEST").Reply()
	if err != nil || !extension.Present {
		conn.Close()
		return nil, errors.New("the X11 XTEST extension is required for input injection")
	}
	if err := xtest.Init(conn); err != nil {
		conn.Close()
		return nil, errors.New("the X11 XTEST extension is required for input injection")
	}

	backend := &Input{
		conn:           conn,
		root:           screen.Root,
		width:          int(geometry.Width),
		height:         int(geometry.Height),
		minKeycode:     int(setup.MinKeycode),
		maxKeycode:     int(setup.MaxKeycode),
		pressedKeys:    make(map[int]bool),
		pressedButtons: make(map[int]bool),
		modifierCache:  make(map[input.Modifiers][]int),
		characterCache: make(map[rune]characterMapping),
	}
	count := backend.maxKeycode - backend.minKeycode + 1
	mapping, err := xproto.GetKeyboardMapping(conn, xproto.Keycode(backend.minKeycode), byte(count)).Reply()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot read the X11 keyboard mapping: %v", err)
	}
	backend.keysymsPerKeycode = int(mapping.KeysymsPerKeycode)
	backend.keysyms = make([]uint32, len(mapping.Keysyms))
	for index, keysym := range mapping.Keysyms {
		backend.keysyms[index] = uint32(keysym)
	}
	return backend, nil
}

// keycodeToKeysym reads the cached keyboard mapping like python-xlib.
func (i *Input) keycodeToKeysym(keycode, level int) uint32 {
	index := keycode - i.minKeycode
	if index < 0 || keycode > i.maxKeycode || level < 0 || level >= i.keysymsPerKeycode {
		return 0
	}
	return i.keysyms[index*i.keysymsPerKeycode+level]
}

func (i *Input) keysymToKeycode(keysym uint32) int {
	return KeysymToKeycode(keysym, i.minKeycode, i.maxKeycode, i.keysymsPerKeycode, i.keycodeToKeysym)
}

// modifierKeycodes resolves the modifier bitmask to keycodes in press order.
func (i *Input) modifierKeycodes(modifiers input.Modifiers) []int {
	if cached, ok := i.modifierCache[modifiers]; ok {
		return cached
	}
	var keycodes []int
	for _, keysym := range ModifierKeysyms(modifiers) {
		keycodes = append(keycodes, i.keysymToKeycode(keysym))
	}
	i.modifierCache[modifiers] = keycodes
	return keycodes
}

// characterKeycode scans the keyboard mapping for a Unicode character.
func (i *Input) characterKeycode(character rune) (int, bool) {
	if cached, ok := i.characterCache[character]; ok {
		return cached.keycode, cached.extraShift
	}
	keycode, extraShift := ScanCharacterKeycode(CharacterKeysym(character), i.minKeycode, i.maxKeycode, i.keycodeToKeysym)
	i.characterCache[character] = characterMapping{keycode: keycode, extraShift: extraShift}
	return keycode, extraShift
}

func (i *Input) fakeKey(press bool, keycode int) {
	eventType := byte(eventKeyRelease)
	if press {
		eventType = eventKeyPress
	}
	xtest.FakeInput(i.conn, eventType, byte(keycode), 0, 0, 0, 0, 0)
}

// Key injects one normalized key event; unsupported events are ignored.
func (i *Input) Key(event input.KeyEvent) {
	if event.Action != input.KeyPress && event.Action != input.KeyRelease && event.Action != input.KeyTap {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.conn == nil {
		return
	}
	var keycode int
	extraShift := false
	if event.Code == input.KeyCharacter {
		if event.Unicode == 0 || event.Unicode > 0x10FFFF {
			return
		}
		keycode, extraShift = i.characterKeycode(event.Unicode)
	} else {
		special, ok := SpecialKeys[event.Code]
		if !ok {
			return
		}
		keycode = i.keysymToKeycode(special.Keysym)
	}
	if keycode == 0 {
		return
	}
	modifiers := event.Modifiers
	if extraShift {
		modifiers |= input.ModShift
	}
	modifierCodes := i.modifierKeycodes(modifiers)
	for _, action := range KeySequence(event.Action, modifierCodes, keycode) {
		i.fakeKey(action.Press, action.Keycode)
	}
	if event.Action == input.KeyPress {
		for _, code := range modifierCodes {
			i.pressedKeys[code] = true
		}
		i.pressedKeys[keycode] = true
	}
	if event.Action == input.KeyRelease || event.Action == input.KeyTap {
		delete(i.pressedKeys, keycode)
		for _, code := range modifierCodes {
			delete(i.pressedKeys, code)
		}
	}
	// xgb writes every request to the wire synchronously, so X11 request
	// order is preserved without an explicit flush.
}

// bounded clamps the pointer position to the desktop.
func (i *Input) bounded(x, y int) (int, int) {
	return ClampCoords(x, y, i.width, i.height)
}

// Move positions the pointer.
func (i *Input) Move(x, y int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.conn == nil {
		return
	}
	x, y = i.bounded(x, y)
	xtest.FakeInput(i.conn, eventMotionNotify, 0, 0, 0, int16(x), int16(y), 0)
}

// Button presses or releases one of the three primary buttons.
func (i *Input) Button(button int, pressed bool, x, y int) {
	if button < 1 || button > 3 {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.conn == nil {
		return
	}
	x, y = i.bounded(x, y)
	xtest.FakeInput(i.conn, eventMotionNotify, 0, 0, 0, int16(x), int16(y), 0)
	eventType := byte(eventButtonRelease)
	if pressed {
		eventType = eventButtonPress
	}
	xtest.FakeInput(i.conn, eventType, byte(button), 0, 0, 0, 0, 0)
	if pressed {
		i.pressedButtons[button] = true
	} else {
		delete(i.pressedButtons, button)
	}
}

// Scroll injects bounded wheel clicks at the clamped position.
func (i *Input) Scroll(amount, x, y int) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.conn == nil {
		return
	}
	x, y = i.bounded(x, y)
	xtest.FakeInput(i.conn, eventMotionNotify, 0, 0, 0, int16(x), int16(y), 0)
	button, repeats := ScrollButton(amount)
	for index := 0; index < repeats; index++ {
		xtest.FakeInput(i.conn, eventButtonPress, byte(button), 0, 0, 0, 0, 0)
		xtest.FakeInput(i.conn, eventButtonRelease, byte(button), 0, 0, 0, 0, 0)
	}
}

// Close releases every held button and key, syncs, and disconnects.
func (i *Input) Close() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.conn == nil {
		return
	}
	for button := range i.pressedButtons {
		xtest.FakeInput(i.conn, eventButtonRelease, byte(button), 0, 0, 0, 0, 0)
	}
	for keycode := range i.pressedKeys {
		xtest.FakeInput(i.conn, eventKeyRelease, byte(keycode), 0, 0, 0, 0, 0)
	}
	if len(i.pressedButtons) > 0 || len(i.pressedKeys) > 0 {
		i.conn.Sync()
	}
	i.pressedButtons = make(map[int]bool)
	i.pressedKeys = make(map[int]bool)
	i.conn.Close()
	i.conn = nil
}
