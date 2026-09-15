package agent

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rarnu/sshdesk-go/internal/capture/synthetic"
	"github.com/rarnu/sshdesk-go/internal/input"
)

type recordedEvent struct {
	kind    string
	event   input.KeyEvent
	button  int
	pressed bool
	amount  int
	x       int
	y       int
}

type recordingInput struct {
	events []recordedEvent
}

func (r *recordingInput) Key(event input.KeyEvent) {
	r.events = append(r.events, recordedEvent{kind: "key", event: event})
}

func (r *recordingInput) Move(x, y int) {
	r.events = append(r.events, recordedEvent{kind: "move", x: x, y: y})
}

func (r *recordingInput) Button(button int, pressed bool, x, y int) {
	r.events = append(r.events, recordedEvent{kind: "button", button: button, pressed: pressed, x: x, y: y})
}

func (r *recordingInput) Scroll(amount, x, y int) {
	r.events = append(r.events, recordedEvent{kind: "scroll", amount: amount, x: x, y: y})
}

func (r *recordingInput) Close() {}

func testController() (*Controller, *recordingInput) {
	controller := NewController()
	backend := &recordingInput{}
	controller.SetBackends(synthetic.NewCapture(320, 180, false), backend)
	return controller, backend
}

func TestObserveAndComputerUseActions(t *testing.T) {
	controller, backend := testController()
	result, err := sessionAction(controller, map[string]any{"action": "observe", "max_width": float64(160)})
	if err != nil {
		t.Fatal(err)
	}
	if result["width"] != 320 || result["height"] != 180 {
		t.Errorf("size = %vx%v, want 320x180", result["width"], result["height"])
	}
	if result["format"] != "png" {
		t.Errorf("format = %v, want png", result["format"])
	}
	if !strings.HasPrefix(result["image_base64"].(string), "iVBOR") {
		t.Error("image_base64 does not look like a PNG")
	}

	mustAction := func(request map[string]any) {
		t.Helper()
		if _, err := sessionAction(controller, request); err != nil {
			t.Fatalf("%v: %v", request["action"], err)
		}
	}
	mustAction(map[string]any{"action": "click", "x": float64(12), "y": float64(34)})
	mustAction(map[string]any{"action": "scroll", "amount": float64(-3), "x": float64(12), "y": float64(34)})
	mustAction(map[string]any{"action": "type", "text": "Hi"})
	mustAction(map[string]any{"action": "key", "key": "enter", "ctrl": true})

	hasEvent := func(match func(recordedEvent) bool) bool {
		for _, event := range backend.events {
			if match(event) {
				return true
			}
		}
		return false
	}
	if !hasEvent(func(e recordedEvent) bool {
		return e.kind == "button" && e.button == 1 && e.pressed && e.x == 12 && e.y == 34
	}) {
		t.Error("missing button press at (12, 34)")
	}
	if !hasEvent(func(e recordedEvent) bool {
		return e.kind == "button" && e.button == 1 && !e.pressed && e.x == 12 && e.y == 34
	}) {
		t.Error("missing button release at (12, 34)")
	}
	if !hasEvent(func(e recordedEvent) bool { return e.kind == "scroll" && e.amount == -3 && e.x == 12 && e.y == 34 }) {
		t.Error("missing scroll -3 at (12, 34)")
	}
	var typed []rune
	for _, event := range backend.events {
		if event.kind == "key" && event.event.Code == input.KeyCharacter {
			typed = append(typed, event.event.Unicode)
		}
	}
	if len(typed) < 2 || typed[0] != 'H' || typed[1] != 'i' {
		t.Errorf("typed characters = %q, want Hi", string(typed))
	}
	last := backend.events[len(backend.events)-1]
	if last.event.Code != input.KeyEnter || last.event.Modifiers != input.ModCtrl {
		t.Errorf("last event = %+v, want ctrl+enter tap", last.event)
	}
}

func TestActionsAreBounded(t *testing.T) {
	controller, _ := testController()
	if _, err := sessionAction(controller, map[string]any{"action": "move", "x": float64(999999), "y": float64(0)}); err == nil || !strings.Contains(err.Error(), "coordinate") {
		t.Errorf("move out of range: err = %v, want coordinate error", err)
	}
	if _, err := sessionAction(controller, map[string]any{"action": "click", "x": float64(0), "y": float64(0), "button": "extra"}); err == nil || !strings.Contains(err.Error(), "button") {
		t.Errorf("click bad button: err = %v, want button error", err)
	}
}

func TestActionClamps(t *testing.T) {
	controller, backend := testController()
	if _, err := sessionAction(controller, map[string]any{"action": "scroll", "amount": float64(99), "x": float64(1), "y": float64(2)}); err != nil {
		t.Fatal(err)
	}
	last := backend.events[len(backend.events)-1]
	if last.kind != "scroll" || last.amount != 20 {
		t.Errorf("scroll amount = %d, want clamped 20", last.amount)
	}
	if _, err := sessionAction(controller, map[string]any{"action": "click", "x": float64(1), "y": float64(2), "count": float64(100)}); err != nil {
		t.Fatal(err)
	}
	presses := 0
	for _, event := range backend.events {
		if event.kind == "button" && event.pressed {
			presses++
		}
	}
	if presses != 20 {
		t.Errorf("click presses = %d, want clamped 20", presses)
	}
	if _, err := sessionAction(controller, map[string]any{"action": "type", "text": strings.Repeat("x", 16385)}); err == nil || !strings.Contains(err.Error(), "limited") {
		t.Errorf("oversized text: err = %v, want length error", err)
	}
}

func TestAgentSSHRejectsUntrustedShellCommands(t *testing.T) {
	cases := []struct {
		command string
		want    int
	}{
		{"id", 126},
		{"sshdesk-agent info; id", 2},
		{"sshdesk-agent && id", 2},
		{"sshdesk-agent screenshot --output /tmp/capture.png", 2},
		{"sshdesk-agent screenshot --output=/tmp/capture.png", 2},
		{"/usr/local/bin/sshdesk-agent unknown-command", 2},
		{"", 126},
		{"sshdesk-agent 'unterminated", 2},
	}
	for _, tc := range cases {
		var stderr bytes.Buffer
		if got := agentSSH([]string{tc.command}, &stderr); got != tc.want {
			t.Errorf("agentSSH(%q) = %d, want %d (stderr: %s)", tc.command, got, tc.want, &stderr)
		}
	}
}

func TestAgentSSHArgCountAndLength(t *testing.T) {
	var stderr bytes.Buffer
	if got := agentSSH(nil, &stderr); got != 2 {
		t.Errorf("no args = %d, want 2", got)
	}
	if got := agentSSH([]string{"sshdesk-agent info", "extra"}, &stderr); got != 2 {
		t.Errorf("two args = %d, want 2", got)
	}
	if got := agentSSH([]string{strings.Repeat("x", 65537)}, &stderr); got != 2 {
		t.Errorf("oversized = %d, want 2", got)
	}
	if got := agentSSH([]string{"id"}, &stderr); got != 126 {
		t.Errorf("rejection = %d, want 126", got)
	}
	if !strings.Contains(stderr.String(), "This account accepts only SSHDESK agent commands.") {
		t.Errorf("rejection message missing: %q", stderr.String())
	}
}

func TestShlexSplit(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"sshdesk-agent info", []string{"sshdesk-agent", "info"}},
		{"  sshdesk-agent   move  1  2 ", []string{"sshdesk-agent", "move", "1", "2"}},
		{`sshdesk-agent type "hello world"`, []string{"sshdesk-agent", "type", "hello world"}},
		{`sshdesk-agent type 'it''s'`, []string{"sshdesk-agent", "type", "its"}},
		{`sshdesk-agent type a\ b`, []string{"sshdesk-agent", "type", "a b"}},
		{`sshdesk-agent type "a\"b"`, []string{"sshdesk-agent", "type", `a"b`}},
		{`sshdesk-agent type "a\nb"`, []string{"sshdesk-agent", "type", `a\nb`}},
		{"", nil},
	}
	for _, tc := range cases {
		got, err := ShlexSplit(tc.input)
		if err != nil {
			t.Errorf("ShlexSplit(%q): %v", tc.input, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("ShlexSplit(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if _, err := ShlexSplit(`"unterminated`); err == nil {
		t.Error("unterminated double quote must fail")
	}
	if _, err := ShlexSplit(`'unterminated`); err == nil {
		t.Error("unterminated single quote must fail")
	}
}
