// Package ydotool implements compositor-independent Linux input by spawning
// the ydotool CLI (ydotoold + /dev/uinput do the real work), mirroring the
// Python YdotoolInput. Command planning is pure logic so it can be tested
// without the daemon; YDOTOOL_SOCKET is honored by the ydotool client
// itself, exactly like the Python version.
package ydotool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rarnu/sshdesk-go/internal/input"
)

// runTimeout mirrors the Python subprocess timeout.
const runTimeout = 2 * time.Second

// SpecialKeys maps normalized key codes to evdev key codes (verbatim from
// the Python SPECIAL_KEYS table).
var SpecialKeys = map[input.KeyCode]int{
	input.KeyEscape:    1,
	input.KeyBackspace: 14,
	input.KeyTab:       15,
	input.KeyEnter:     28,
	input.KeyF1:        59,
	input.KeyF2:        60,
	input.KeyF3:        61,
	input.KeyF4:        62,
	input.KeyF5:        63,
	input.KeyF6:        64,
	input.KeyF7:        65,
	input.KeyF8:        66,
	input.KeyF9:        67,
	input.KeyF10:       68,
	input.KeyF11:       87,
	input.KeyF12:       88,
	input.KeyHome:      102,
	input.KeyUp:        103,
	input.KeyPageUp:    104,
	input.KeyLeft:      105,
	input.KeyRight:     106,
	input.KeyEnd:       107,
	input.KeyDown:      108,
	input.KeyPageDown:  109,
	input.KeyInsert:    110,
	input.KeyDelete:    111,
}

// LetterKeys maps lowercase letters to evdev key codes by keyboard row.
var LetterKeys = func() map[rune]int {
	keys := make(map[rune]int)
	for index, letter := range "asdfghjkl" {
		keys[letter] = 30 + index
	}
	for index, letter := range "qwertyuiop" {
		keys[letter] = 16 + index
	}
	for index, letter := range "zxcvbnm" {
		keys[letter] = 44 + index
	}
	return keys
}()

// ModifierCodes mirrors the Python _modifier_codes: the table iterates in
// CTRL(29), SHIFT(42), ALT(56) order.
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

// KeyPlan is the resolved ydotool invocation for one key event.
type KeyPlan struct {
	Arguments []string // argv after the executable; nil means the event is ignored
	Pressed   []int    // evdev codes entering the pressed set (KeyPress only)
	Released  []int    // evdev codes leaving the pressed set (release/tap)
}

// PlanKey mirrors YdotoolInput.key: modifier presses in order, the main key
// press, the main key release, then the modifier releases in reverse. A
// bare character tap with no evdev code falls back to `ydotool type`.
func PlanKey(event input.KeyEvent) KeyPlan {
	if event.Action != input.KeyPress && event.Action != input.KeyRelease && event.Action != input.KeyTap {
		return KeyPlan{}
	}
	character := rune(0)
	code, hasCode := SpecialKeys[event.Code]
	if event.Code == input.KeyCharacter && event.Unicode > 0 && event.Unicode <= 0x10FFFF {
		character = event.Unicode
		code, hasCode = LetterKeys[unicode.ToLower(character)]
	}

	modifierBits := event.Modifiers
	if unicode.IsUpper(character) {
		modifierBits |= input.ModShift
	}
	modifiers := ModifierCodes(modifierBits)
	if !hasCode && character != 0 && event.Action == input.KeyTap && len(modifiers) == 0 {
		return KeyPlan{Arguments: []string{"type", "--key-delay", "0", "--", string(character)}}
	}
	if !hasCode {
		return KeyPlan{}
	}

	var sequence []string
	if event.Action == input.KeyPress || event.Action == input.KeyTap {
		for _, modifier := range modifiers {
			sequence = append(sequence, strconv.Itoa(modifier)+":1")
		}
		sequence = append(sequence, strconv.Itoa(code)+":1")
	}
	if event.Action == input.KeyRelease || event.Action == input.KeyTap {
		sequence = append(sequence, strconv.Itoa(code)+":0")
		for index := len(modifiers) - 1; index >= 0; index-- {
			sequence = append(sequence, strconv.Itoa(modifiers[index])+":0")
		}
	}
	plan := KeyPlan{Arguments: append([]string{"key", "--key-delay", "0"}, sequence...)}
	if event.Action == input.KeyPress {
		plan.Pressed = append(append([]int{}, modifiers...), code)
	}
	if event.Action == input.KeyRelease || event.Action == input.KeyTap {
		plan.Released = append([]int{code}, modifiers...)
	}
	return plan
}

// buttonBases maps the normalized buttons to the ydotool click base codes.
var buttonBases = map[int]int{1: 0, 2: 2, 3: 1}

// MoveArguments is the absolute pointer move vector.
func MoveArguments(x, y int) []string {
	return []string{"mousemove", "--absolute", strconv.Itoa(max(0, x)), strconv.Itoa(max(0, y))}
}

// ClickArguments is the button press/release vector (0x40 down, 0x80 up).
func ClickArguments(button int, pressed bool) []string {
	mask := 0x80
	if pressed {
		mask = 0x40
	}
	return []string{"click", "--next-delay", "0", fmt.Sprintf("%#x", mask|buttonBases[button])}
}

// ScrollArguments is the wheel vector; the amount is clamped to ±20.
func ScrollArguments(amount int) []string {
	return []string{"mousemove", "--wheel", "0", strconv.Itoa(max(-20, min(20, amount)))}
}

// runner spawns one ydotool invocation; it is a field seam for tests.
type runner func(argv []string) error

// lookPath and defaultRunner are package seams so tests can drive New.
var (
	lookPath      = exec.LookPath
	defaultRunner = runner(execRunner)
)

// execRunner runs ydotool with a null stdin/stdout and captured stderr.
func execRunner(argv []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("ydotool input timed out after %s seconds", strconv.FormatFloat(runTimeout.Seconds(), 'g', -1, 64))
	}
	if err != nil {
		detail := strings.TrimSpace(string(bytes.ToValidUTF8(stderr.Bytes(), []byte("�"))))
		if detail == "" {
			detail = "is ydotoold running?"
		}
		return fmt.Errorf("ydotool input failed: %s", detail)
	}
	return nil
}

// Input mirrors the Python YdotoolInput.
type Input struct {
	Executable string

	run            runner
	pressedButtons map[int]bool
	pressedKeys    map[int]bool
}

var _ input.Backend = (*Input)(nil)

// New locates the ydotool client and probes the daemon with `ydotool debug`
// (which injects nothing), so `sshdesk-server --check` verifies the socket
// and its permissions.
func New() (*Input, error) {
	executable, err := lookPath("ydotool")
	if err != nil {
		return nil, errors.New("Wayland input needs ydotool and a running ydotoold with /dev/uinput access")
	}
	backend := &Input{
		Executable:     executable,
		run:            defaultRunner,
		pressedButtons: make(map[int]bool),
		pressedKeys:    make(map[int]bool),
	}
	if err := backend.run([]string{executable, "debug"}); err != nil {
		return nil, err
	}
	return backend, nil
}

func (i *Input) runnerFunc() runner {
	if i.run != nil {
		return i.run
	}
	return execRunner
}

// Key injects one normalized key event; unsupported events are ignored.
// The Backend interface returns no error, so daemon failures after the
// constructor probe are dropped silently (the Python version would raise).
func (i *Input) Key(event input.KeyEvent) {
	plan := PlanKey(event)
	if plan.Arguments == nil {
		return
	}
	_ = i.runnerFunc()(append([]string{i.Executable}, plan.Arguments...))
	for _, code := range plan.Pressed {
		i.pressedKeys[code] = true
	}
	for _, code := range plan.Released {
		delete(i.pressedKeys, code)
	}
}

// Move positions the pointer.
func (i *Input) Move(x, y int) {
	_ = i.runnerFunc()(append([]string{i.Executable}, MoveArguments(x, y)...))
}

// Button moves the pointer and presses or releases one of the three primary
// buttons.
func (i *Input) Button(button int, pressed bool, x, y int) {
	if button < 1 || button > 3 {
		return
	}
	i.Move(x, y)
	_ = i.runnerFunc()(append([]string{i.Executable}, ClickArguments(button, pressed)...))
	if pressed {
		i.pressedButtons[button] = true
	} else {
		delete(i.pressedButtons, button)
	}
}

// Scroll moves the pointer and injects a bounded wheel event.
func (i *Input) Scroll(amount, x, y int) {
	i.Move(x, y)
	_ = i.runnerFunc()(append([]string{i.Executable}, ScrollArguments(amount)...))
}

// Close releases every held button and key, swallowing daemon errors like
// the Python close().
func (i *Input) Close() {
	run := i.runnerFunc()
	for button := range i.pressedButtons {
		_ = run(append([]string{i.Executable}, ClickArguments(button, false)...))
	}
	if len(i.pressedKeys) > 0 {
		sequence := []string{i.Executable, "key", "--key-delay", "0"}
		for code := range i.pressedKeys {
			sequence = append(sequence, strconv.Itoa(code)+":0")
		}
		_ = run(sequence)
	}
	i.pressedButtons = make(map[int]bool)
	i.pressedKeys = make(map[int]bool)
}
