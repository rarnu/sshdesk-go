package ydotool

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/rarnu/sshdesk-go/internal/input"
)

func TestSpecialKeysTable(t *testing.T) {
	if len(SpecialKeys) != 26 {
		t.Fatalf("SpecialKeys has %d entries, want 26", len(SpecialKeys))
	}
	spot := map[input.KeyCode]int{
		input.KeyEscape:    1,
		input.KeyBackspace: 14,
		input.KeyTab:       15,
		input.KeyEnter:     28,
		input.KeyF1:        59,
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
	for code, want := range spot {
		if got := SpecialKeys[code]; got != want {
			t.Fatalf("SpecialKeys[%d] = %d, want %d", code, got, want)
		}
	}
}

func TestLetterKeysRows(t *testing.T) {
	spot := map[rune]int{
		'q': 16, 'w': 17, 'p': 25,
		'a': 30, 's': 31, 'l': 38,
		'z': 44, 'x': 45, 'm': 50,
	}
	for letter, want := range spot {
		if got := LetterKeys[letter]; got != want {
			t.Fatalf("LetterKeys[%q] = %d, want %d", letter, got, want)
		}
	}
	if len(LetterKeys) != 26 {
		t.Fatalf("LetterKeys has %d entries, want 26", len(LetterKeys))
	}
}

func TestModifierCodesOrder(t *testing.T) {
	if got := ModifierCodes(input.ModCtrl | input.ModShift | input.ModAlt); !reflect.DeepEqual(got, []int{29, 42, 56}) {
		t.Fatalf("ctrl+shift+alt = %v, want [29 42 56]", got)
	}
	if got := ModifierCodes(input.ModAlt); !reflect.DeepEqual(got, []int{56}) {
		t.Fatalf("alt = %v", got)
	}
	if got := ModifierCodes(input.ModNone); len(got) != 0 {
		t.Fatalf("none = %v", got)
	}
}

func TestPlanKeyTapLetter(t *testing.T) {
	plan := PlanKey(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: 'a'})
	want := []string{"key", "--key-delay", "0", "30:1", "30:0"}
	if !reflect.DeepEqual(plan.Arguments, want) {
		t.Fatalf("tap 'a' = %q, want %q", plan.Arguments, want)
	}
}

func TestPlanKeyUppercaseAddsShift(t *testing.T) {
	plan := PlanKey(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: 'A'})
	want := []string{"key", "--key-delay", "0", "42:1", "30:1", "30:0", "42:0"}
	if !reflect.DeepEqual(plan.Arguments, want) {
		t.Fatalf("tap 'A' = %q, want %q", plan.Arguments, want)
	}
}

func TestPlanKeySpecialWithModifier(t *testing.T) {
	plan := PlanKey(input.KeyEvent{Action: input.KeyTap, Modifiers: input.ModCtrl, Code: input.KeyEnter})
	want := []string{"key", "--key-delay", "0", "29:1", "28:1", "28:0", "29:0"}
	if !reflect.DeepEqual(plan.Arguments, want) {
		t.Fatalf("ctrl+enter = %q, want %q", plan.Arguments, want)
	}
}

func TestPlanKeyPressAndRelease(t *testing.T) {
	press := PlanKey(input.KeyEvent{Action: input.KeyPress, Code: input.KeyTab})
	if !reflect.DeepEqual(press.Arguments, []string{"key", "--key-delay", "0", "15:1"}) {
		t.Fatalf("press = %q", press.Arguments)
	}
	if !reflect.DeepEqual(press.Pressed, []int{15}) || len(press.Released) != 0 {
		t.Fatalf("press tracking = %+v", press)
	}
	release := PlanKey(input.KeyEvent{Action: input.KeyRelease, Modifiers: input.ModShift, Code: input.KeyTab})
	if !reflect.DeepEqual(release.Arguments, []string{"key", "--key-delay", "0", "15:0", "42:0"}) {
		t.Fatalf("release = %q", release.Arguments)
	}
	if !reflect.DeepEqual(release.Released, []int{15, 42}) {
		t.Fatalf("release tracking = %+v", release)
	}
}

func TestPlanKeyTypeFallback(t *testing.T) {
	plan := PlanKey(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: '5'})
	want := []string{"type", "--key-delay", "0", "--", "5"}
	if !reflect.DeepEqual(plan.Arguments, want) {
		t.Fatalf("tap '5' = %q, want %q", plan.Arguments, want)
	}
}

func TestPlanKeyIgnored(t *testing.T) {
	cases := []input.KeyEvent{
		{Action: 9, Code: input.KeyEnter},
		{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: 0},
		{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: 0x110000},
		{Action: input.KeyTap, Code: input.KeyCode(15)},
		{Action: input.KeyPress, Code: input.KeyCharacter, Unicode: '5'},
		{Action: input.KeyTap, Modifiers: input.ModShift, Code: input.KeyCharacter, Unicode: '5'},
	}
	for index, event := range cases {
		if plan := PlanKey(event); plan.Arguments != nil {
			t.Fatalf("case %d: PlanKey(%+v) = %q, want ignored", index, event, plan.Arguments)
		}
	}
}

func TestPointerVectors(t *testing.T) {
	if got := MoveArguments(-5, 12); !reflect.DeepEqual(got, []string{"mousemove", "--absolute", "0", "12"}) {
		t.Fatalf("move = %q", got)
	}
	if got := ClickArguments(1, true); !reflect.DeepEqual(got, []string{"click", "--next-delay", "0", "0x40"}) {
		t.Fatalf("left press = %q", got)
	}
	if got := ClickArguments(2, false); !reflect.DeepEqual(got, []string{"click", "--next-delay", "0", "0x82"}) {
		t.Fatalf("middle release = %q", got)
	}
	if got := ClickArguments(3, true); !reflect.DeepEqual(got, []string{"click", "--next-delay", "0", "0x41"}) {
		t.Fatalf("right press = %q", got)
	}
	if got := ScrollArguments(99); !reflect.DeepEqual(got, []string{"mousemove", "--wheel", "0", "20"}) {
		t.Fatalf("scroll up = %q", got)
	}
	if got := ScrollArguments(-3); !reflect.DeepEqual(got, []string{"mousemove", "--wheel", "0", "-3"}) {
		t.Fatalf("scroll down = %q", got)
	}
}

// recordingInput builds an Input with a scripted runner.
func recordingInput(calls *[][]string) *Input {
	return &Input{
		Executable: "/usr/bin/ydotool",
		run: func(argv []string) error {
			*calls = append(*calls, append([]string{}, argv...))
			return nil
		},
		pressedButtons: make(map[int]bool),
		pressedKeys:    make(map[int]bool),
	}
}

func TestKeyTracksPressedCodes(t *testing.T) {
	var calls [][]string
	backend := recordingInput(&calls)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Modifiers: input.ModCtrl, Code: input.KeyEnter})
	if !backend.pressedKeys[28] || !backend.pressedKeys[29] {
		t.Fatalf("pressed keys = %v", backend.pressedKeys)
	}
	backend.Key(input.KeyEvent{Action: input.KeyRelease, Modifiers: input.ModCtrl, Code: input.KeyEnter})
	if len(backend.pressedKeys) != 0 {
		t.Fatalf("pressed keys after release = %v", backend.pressedKeys)
	}
	if len(calls) != 2 {
		t.Fatalf("runner calls = %q", calls)
	}
}

func TestButtonMovesClicksAndTracks(t *testing.T) {
	var calls [][]string
	backend := recordingInput(&calls)
	backend.Button(1, true, 10, 20)
	if !reflect.DeepEqual(calls[0], []string{"/usr/bin/ydotool", "mousemove", "--absolute", "10", "20"}) {
		t.Fatalf("move call = %q", calls[0])
	}
	if !reflect.DeepEqual(calls[1], []string{"/usr/bin/ydotool", "click", "--next-delay", "0", "0x40"}) {
		t.Fatalf("click call = %q", calls[1])
	}
	if !backend.pressedButtons[1] {
		t.Fatal("button 1 not tracked")
	}
	backend.Button(9, true, 0, 0)
	if len(calls) != 2 {
		t.Fatalf("unsupported button spawned calls: %q", calls)
	}
}

func TestCloseReleasesHeldState(t *testing.T) {
	var calls [][]string
	backend := recordingInput(&calls)
	backend.Button(1, true, 0, 0)
	backend.Key(input.KeyEvent{Action: input.KeyPress, Code: input.KeyEnter})
	calls = nil
	backend.Close()
	if len(calls) != 2 {
		t.Fatalf("close calls = %q", calls)
	}
	if !reflect.DeepEqual(calls[0], []string{"/usr/bin/ydotool", "click", "--next-delay", "0", "0x80"}) {
		t.Fatalf("button release = %q", calls[0])
	}
	if !reflect.DeepEqual(calls[1], []string{"/usr/bin/ydotool", "key", "--key-delay", "0", "28:0"}) {
		t.Fatalf("key release = %q", calls[1])
	}
	if len(backend.pressedButtons) != 0 || len(backend.pressedKeys) != 0 {
		t.Fatal("close did not clear the pressed sets")
	}
}

func TestCloseSwallowsDaemonErrors(t *testing.T) {
	backend := &Input{
		Executable: "/usr/bin/ydotool",
		run: func(argv []string) error {
			return errors.New("ydotool input failed: boom")
		},
		pressedButtons: map[int]bool{1: true},
		pressedKeys:    map[int]bool{28: true},
	}
	backend.Close()
	if len(backend.pressedButtons) != 0 || len(backend.pressedKeys) != 0 {
		t.Fatal("close did not clear the pressed sets")
	}
}

func TestNewProbesDaemonWithoutInjecting(t *testing.T) {
	restoreLookPath, restoreRunner := lookPath, defaultRunner
	t.Cleanup(func() { lookPath, defaultRunner = restoreLookPath, restoreRunner })
	lookPath = func(name string) (string, error) { return "/usr/bin/ydotool", nil }
	var calls [][]string
	defaultRunner = func(argv []string) error {
		calls = append(calls, append([]string{}, argv...))
		return nil
	}
	backend, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if backend.Executable != "/usr/bin/ydotool" {
		t.Fatalf("Executable = %q", backend.Executable)
	}
	if !reflect.DeepEqual(calls, [][]string{{"/usr/bin/ydotool", "debug"}}) {
		t.Fatalf("probe calls = %q, want a single debug probe", calls)
	}
}

func TestNewReportsMissingExecutable(t *testing.T) {
	restoreLookPath := lookPath
	t.Cleanup(func() { lookPath = restoreLookPath })
	lookPath = func(name string) (string, error) { return "", errors.New("not found") }
	_, err := New()
	want := "Wayland input needs ydotool and a running ydotoold with /dev/uinput access"
	if err == nil || err.Error() != want {
		t.Fatalf("New() error = %v, want %q", err, want)
	}
}

func TestNewReportsDaemonProbeFailure(t *testing.T) {
	restoreLookPath, restoreRunner := lookPath, defaultRunner
	t.Cleanup(func() { lookPath, defaultRunner = restoreLookPath, restoreRunner })
	lookPath = func(name string) (string, error) { return "/usr/bin/ydotool", nil }
	defaultRunner = func(argv []string) error {
		return errors.New("ydotool input failed: is ydotoold running?")
	}
	if _, err := New(); err == nil || !strings.Contains(err.Error(), "is ydotoold running?") {
		t.Fatalf("New() error = %v, want the daemon hint", err)
	}
}
