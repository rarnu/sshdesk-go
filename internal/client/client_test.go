package client

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSplitCommandIsAnArgumentVector(t *testing.T) {
	want := []string{"split-window", "-h", "-p", "50", "-t", "%3", "--", "ssh", "-t", "alice@example.com", "desktop"}
	if got := splitArguments("alice@example.com", "right", 50, "%3"); !reflect.DeepEqual(got, want) {
		t.Errorf("splitArguments = %v, want %v", got, want)
	}
	up := splitArguments("host", "up", 30, "%1")
	if !reflect.DeepEqual(up, []string{"split-window", "-v", "-b", "-p", "30", "-t", "%1", "--", "ssh", "-t", "host", "desktop"}) {
		t.Errorf("up split = %v", up)
	}
}

type fakeRun struct {
	argv    []string
	stdin   []byte
	timeout float64
	stdout  []byte
	stderr  []byte
	code    int
	err     error
}

func fakeSSH(stdout []byte, code int) (*fakeRun, func()) {
	run := &fakeRun{stdout: stdout, code: code}
	oldRun, oldLook := runSeam, lookPathSeam
	runSeam = func(argv []string, stdinBytes []byte, timeout float64) ([]byte, []byte, int, error) {
		run.argv = argv
		run.stdin = stdinBytes
		run.timeout = timeout
		return run.stdout, run.stderr, run.code, run.err
	}
	lookPathSeam = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	return run, func() { runSeam, lookPathSeam = oldRun, oldLook }
}

func TestRemoteRequestUsesOnlyFixedSSHCommand(t *testing.T) {
	run, restore := fakeSSH([]byte("{\"id\":1,\"ok\":true,\"width\":320}\n"), 0)
	defer restore()
	response, err := remoteRequest("alice@example.com", map[string]any{"id": 1, "action": "info"}, defaultRequestTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if response["width"] != float64(320) {
		t.Errorf("width = %v, want 320", response["width"])
	}
	want := []string{"/usr/bin/ssh", "alice@example.com", "sshdesk-agent", "session"}
	if !reflect.DeepEqual(run.argv, want) {
		t.Errorf("ssh argv = %v, want %v", run.argv, want)
	}
	if run.timeout != 30.0 {
		t.Errorf("timeout = %v, want 30", run.timeout)
	}
	if string(run.stdin) != "{\"action\":\"info\",\"id\":1}\n" {
		t.Errorf("payload = %q", run.stdin)
	}
}

func TestRemoteRequestTimeoutIsConfigurable(t *testing.T) {
	run, restore := fakeSSH([]byte("{\"ok\":true}\n"), 0)
	defer restore()
	if _, err := remoteRequest("alice@example.com", map[string]any{"id": 1, "action": "observe"}, 120.0); err != nil {
		t.Fatal(err)
	}
	if run.timeout != 120.0 {
		t.Errorf("timeout = %v, want 120", run.timeout)
	}
}

func TestRemoteReportsTimeoutExpiryWithoutTraceback(t *testing.T) {
	run, restore := fakeSSH(nil, 0)
	defer restore()
	run.err = TimeoutError{Seconds: 45}
	var stderr bytes.Buffer
	if got := remoteMain([]string{"alice@example.com", "info"}, strings.NewReader(""), &bytes.Buffer{}, &stderr); got != 1 {
		t.Errorf("exit = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), "timed out after 45 seconds") {
		t.Errorf("stderr = %q, want timeout message", stderr.String())
	}
}

func TestRemoteRejectsNonPositiveTimeout(t *testing.T) {
	var stderr bytes.Buffer
	if got := remoteMain([]string{"alice@example.com", "--timeout", "0", "info"}, nil, &bytes.Buffer{}, &stderr); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "--timeout must be a positive number of seconds") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRemoteRejectsBadTarget(t *testing.T) {
	bad := []string{"host;rm -rf /", string([]byte{'a', 0})}
	for _, target := range bad {
		var stderr bytes.Buffer
		if got := remoteMain([]string{target, "info"}, nil, &bytes.Buffer{}, &stderr); got != 2 {
			t.Errorf("target %q: exit = %d, want 2", target, got)
		}
	}
	long := strings.Repeat("a", 256)
	var stderr bytes.Buffer
	if got := remoteMain([]string{long, "info"}, nil, &bytes.Buffer{}, &stderr); got != 2 {
		t.Errorf("256-char target: exit = %d, want 2", got)
	}
}

func TestRemoteRequestRejectsOversizedResponse(t *testing.T) {
	_, restore := fakeSSH(make([]byte, maxRemoteResponse+1), 0)
	defer restore()
	if _, err := remoteRequest("alice@example.com", map[string]any{"id": 1, "action": "observe"}, 30); err == nil ||
		!strings.Contains(err.Error(), "safety limit") {
		t.Errorf("err = %v, want safety limit error", err)
	}
}

func TestRemoteRequestSurfacesSSHFailure(t *testing.T) {
	run, restore := fakeSSH(nil, 255)
	defer restore()
	run.stderr = []byte("ssh: connect to host: Connection refused\n")
	_, err := remoteRequest("alice@example.com", map[string]any{"id": 1, "action": "info"}, 30)
	if err == nil || !strings.Contains(err.Error(), "Connection refused") {
		t.Errorf("err = %v, want stderr detail", err)
	}
	run.stderr = nil
	_, err = remoteRequest("alice@example.com", map[string]any{"id": 1, "action": "info"}, 30)
	if err == nil || !strings.Contains(err.Error(), "SSH exited with status 255") {
		t.Errorf("err = %v, want exit status detail", err)
	}
}

func TestRemoteRequestSurfacesAgentError(t *testing.T) {
	_, restore := fakeSSH([]byte("{\"id\":1,\"ok\":false,\"error\":\"unknown action: teleport\"}\n"), 0)
	defer restore()
	_, err := remoteRequest("alice@example.com", map[string]any{"id": 1, "action": "teleport"}, 30)
	if err == nil || err.Error() != "unknown action: teleport" {
		t.Errorf("err = %v", err)
	}
}

func TestRemoteInfoPrintsIndentedJSON(t *testing.T) {
	_, restore := fakeSSH([]byte("{\"id\":1,\"ok\":true,\"width\":320,\"height\":180}\n"), 0)
	defer restore()
	var stdout, stderr bytes.Buffer
	if got := remoteMain([]string{"alice@example.com", "info"}, nil, &stdout, &stderr); got != 0 {
		t.Fatalf("exit = %d (stderr %s)", got, &stderr)
	}
	out := stdout.String()
	if !strings.Contains(out, "\n  \"width\": 320") || strings.Contains(out, "\"ok\"") || strings.Contains(out, "\"id\"") {
		t.Errorf("info output = %q", out)
	}
}

func TestRemoteScreenshotDecodesBase64(t *testing.T) {
	// 1x1 transparent PNG, base64 "iVBOR..." is checked by the agent tests;
	// here any decodable payload proves the write path.
	_, restore := fakeSSH([]byte("{\"id\":1,\"ok\":true,\"image_base64\":\"aGVsbG8=\"}\n"), 0)
	defer restore()
	var stdout bytes.Buffer
	if got := remoteMain([]string{"alice@example.com", "screenshot"}, nil, &stdout, &bytes.Buffer{}); got != 0 {
		t.Fatalf("exit = %d", got)
	}
	if stdout.String() != "hello" {
		t.Errorf("stdout = %q, want decoded image bytes", stdout.String())
	}
}

func TestBuildRequestFields(t *testing.T) {
	request, _, err := buildRequest("click", []string{"10", "20", "--button", "right", "--count", "3"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"id": 1, "action": "click", "x": 10, "y": 20, "button": "right", "count": 3}
	if !reflect.DeepEqual(request, want) {
		t.Errorf("request = %v, want %v", request, want)
	}
	if _, _, err := buildRequest("nonsense", nil); err == nil {
		t.Error("unknown subcommand must fail")
	}
	var usage usageError
	if _, _, err := buildRequest("nonsense", nil); !errors.As(err, &usage) {
		t.Errorf("err = %v, want usageError", err)
	}
}

func TestSplitArgvValidation(t *testing.T) {
	if _, _, _, err := parseSplitArgv([]string{"host", "--direction", "sideways"}); err == nil {
		t.Error("bad direction must fail")
	}
	if _, _, _, err := parseSplitArgv([]string{"--size", "abc", "host"}); err == nil {
		t.Error("bad size must fail")
	}
	target, direction, size, err := parseSplitArgv([]string{"--direction=left", "--size", "60", "host"})
	if err != nil || target != "host" || direction != "left" || size != 60 {
		t.Errorf("parsed = %q %q %d %v", target, direction, size, err)
	}
}

func TestSplitRunsInsideTmux(t *testing.T) {
	var stderr bytes.Buffer
	var calls [][]string
	oldRun, oldLook, oldGetenv := tmuxRun, lookPathSeam, tmuxGetenv
	defer func() { tmuxRun, lookPathSeam, tmuxGetenv = oldRun, oldLook, oldGetenv }()
	tmuxRun = func(tmux string, args ...string) int {
		calls = append(calls, append([]string{tmux}, args...))
		return 0
	}
	lookPathSeam = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	tmuxGetenv = func(name string) string {
		if name == "TMUX" {
			return "/tmp/tmux-1000/default,1,0"
		}
		if name == "TMUX_PANE" {
			return "%3"
		}
		return ""
	}
	if got := splitMain([]string{"alice@example.com"}, &stderr); got != 0 {
		t.Fatalf("exit = %d (stderr %s)", got, &stderr)
	}
	if len(calls) != 2 {
		t.Fatalf("tmux calls = %v", calls)
	}
	wantFirst := []string{"/usr/bin/tmux", "set-option", "-p", "-t", "%3", "allow-passthrough", "on"}
	if !reflect.DeepEqual(calls[0], wantFirst) {
		t.Errorf("first call = %v, want %v", calls[0], wantFirst)
	}
	wantSecond := []string{"/usr/bin/tmux", "split-window", "-h", "-p", "50", "-t", "%3", "--", "ssh", "-t", "alice@example.com", "desktop"}
	if !reflect.DeepEqual(calls[1], wantSecond) {
		t.Errorf("second call = %v, want %v", calls[1], wantSecond)
	}
}

func TestSplitRequiresTmuxBinary(t *testing.T) {
	var stderr bytes.Buffer
	oldLook := lookPathSeam
	defer func() { lookPathSeam = oldLook }()
	lookPathSeam = func(name string) (string, error) { return "", errors.New("not found") }
	if got := splitMain([]string{"host"}, &stderr); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
	if !strings.Contains(stderr.String(), "tmux is required") {
		t.Errorf("stderr = %q", stderr.String())
	}
}
