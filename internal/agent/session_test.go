package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func runSession(controller *Controller, lines []string) (int, []map[string]any) {
	var payload strings.Builder
	for _, line := range lines {
		payload.WriteString(line)
		payload.WriteString("\n")
	}
	var output bytes.Buffer
	code := RunSession(controller, strings.NewReader(payload.String()), &output)
	var responses []map[string]any
	for _, line := range strings.Split(strings.TrimRight(output.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			panic(fmt.Sprintf("response is not JSON: %q", line))
		}
		responses = append(responses, decoded)
	}
	return code, responses
}

func TestInfoAndQuitStopTheSession(t *testing.T) {
	controller, _ := testController()
	controller.SetPlatform(func() (PlatformInfo, error) {
		return PlatformInfo{System: "Linux", Session: "x11", Capture: "synthetic", Input: "null"}, nil
	})
	quitLine := `{"id":1,"action":"quit"}`
	infoLine := `{"id":2,"action":"info"}`
	code, responses := runSession(controller, []string{infoLine, quitLine, infoLine})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(responses) != 2 {
		t.Fatalf("responses = %d, want 2 (session must stop after quit)", len(responses))
	}
	if responses[0]["ok"] != true {
		t.Errorf("info response not ok: %v", responses[0])
	}
	if responses[0]["width"] != float64(320) {
		t.Errorf("width = %v, want 320", responses[0]["width"])
	}
	if responses[0]["platform"] != "Linux" || responses[0]["session"] != "x11" ||
		responses[0]["capture"] != "synthetic" || responses[0]["input"] != "null" {
		t.Errorf("platform fields = %v", responses[0])
	}
	if responses[1]["quit"] != true {
		t.Errorf("quit response = %v", responses[1])
	}
	if responses[1]["id"] != float64(1) {
		t.Errorf("quit id = %v, want 1", responses[1]["id"])
	}
}

func TestErrorsKeepTheSessionAlive(t *testing.T) {
	controller, backend := testController()
	lines := []string{
		"{not json",
		"[1, 2]",
		`{"id":3,"action":"teleport"}`,
		`{"id":4,"action":"move","x":999999,"y":0}`,
		`{"id":5,"action":"move","x":7,"y":9}`,
	}
	code, responses := runSession(controller, lines)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(responses) != len(lines) {
		t.Fatalf("responses = %d, want %d", len(responses), len(lines))
	}
	if responses[0]["ok"] != false {
		t.Error("bad JSON must fail")
	}
	if id, present := responses[0]["id"]; !present || id != nil {
		t.Errorf("bad JSON id = %v (present %v), want explicit null", id, present)
	}
	if !strings.Contains(responses[1]["error"].(string), "JSON object") {
		t.Errorf("array error = %v, want JSON object message", responses[1]["error"])
	}
	if !strings.Contains(responses[2]["error"].(string), "unknown action") {
		t.Errorf("teleport error = %v, want unknown action", responses[2]["error"])
	}
	if !strings.Contains(responses[3]["error"].(string), "coordinate") {
		t.Errorf("move error = %v, want coordinate message", responses[3]["error"])
	}
	if responses[4]["ok"] != true {
		t.Errorf("final move must succeed: %v", responses[4])
	}
	found := false
	for _, event := range backend.events {
		if event.kind == "move" && event.x == 7 && event.y == 9 {
			found = true
		}
	}
	if !found {
		t.Error("move (7, 9) was not injected")
	}
}

func TestOversizedRequestsAreRejectedWithoutParsing(t *testing.T) {
	controller, backend := testController()
	oversized := strings.Repeat("x", maxCommandLength+1)
	code, responses := runSession(controller, []string{oversized, `{"id":6,"action":"quit"}`})
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if len(responses) != 2 {
		t.Fatalf("responses = %d, want 2", len(responses))
	}
	if responses[0]["ok"] != false || responses[0]["error"] != "request is too large" {
		t.Errorf("oversized response = %v", responses[0])
	}
	if _, present := responses[0]["id"]; present {
		t.Errorf("oversized response must not carry an id: %v", responses[0])
	}
	if len(backend.events) != 0 {
		t.Errorf("oversized request must not inject input: %v", backend.events)
	}
}

func TestUnknownActionMessage(t *testing.T) {
	controller, _ := testController()
	_, responses := runSession(controller, []string{`{"id":9,"action":"teleport"}`})
	if responses[0]["error"] != "unknown action: teleport" {
		t.Errorf("error = %v, want exact unknown action message", responses[0]["error"])
	}
}
