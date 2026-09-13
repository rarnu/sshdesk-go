package forcedcmd

import (
	"bytes"
	"io"
	"reflect"
	"testing"
)

type fakeDeps struct {
	env        map[string]string
	config     map[string]string
	configErr  error
	account    string
	hasTTY     bool
	execCalls  [][]string
	serverHits int
	agentHits  [][]string
	shell      string
	stderr     bytes.Buffer
}

func (f *fakeDeps) deps() Deps {
	return Deps{
		Getenv:  func(name string) string { return f.env[name] },
		Account: func() (string, error) { return f.account, nil },
		Config: func() (map[string]string, error) {
			if f.configErr != nil {
				return nil, f.configErr
			}
			return f.config, nil
		},
		HasTerminal: func() bool { return f.hasTTY },
		Exec: func(argv []string) (int, error) {
			f.execCalls = append(f.execCalls, argv)
			return 0, nil
		},
		ServerMain: func() int {
			f.serverHits++
			return 0
		},
		AgentSSHMain: func(argv []string) int {
			f.agentHits = append(f.agentHits, argv)
			return 126
		},
		LoginShell: func() (string, error) { return f.shell, nil },
		SelfPath:   func() (string, error) { return "/usr/local/bin/sshdesk", nil },
		Stderr:     &f.stderr,
	}
}

func newFake(command string) *fakeDeps {
	return &fakeDeps{
		env:     map[string]string{"SSH_ORIGINAL_COMMAND": command},
		config:  map[string]string{"RUN_AS": "alice"},
		account: "alice",
		hasTTY:  true,
		shell:   "/bin/bash",
	}
}

func TestRoutesOnlyAgentGrammar(t *testing.T) {
	f := newFake("id")
	if got := Main(f.deps()); got != 126 {
		t.Errorf("exit = %d, want 126", got)
	}
	if len(f.agentHits) != 1 || f.agentHits[0][0] != "id" {
		t.Errorf("agent hits = %v", f.agentHits)
	}
	if f.serverHits != 0 || len(f.execCalls) != 0 {
		t.Error("desktop path must not run for agent commands")
	}
}

func TestOpensShellSelector(t *testing.T) {
	f := newFake("shell")
	if got := Main(f.deps()); got != 0 {
		t.Errorf("exit = %d, want 0 (stderr %s)", got, &f.stderr)
	}
	want := []string{"-bash"}
	if isWindows {
		want = []string{"/bin/bash"}
	}
	if len(f.execCalls) != 1 || !reflect.DeepEqual(f.execCalls[0], want) {
		t.Errorf("exec calls = %v, want [%v]", f.execCalls, want)
	}
	if f.serverHits != 0 || len(f.agentHits) != 0 {
		t.Error("shell selector must not touch desktop or agent paths")
	}
}

func TestShellSelectorRequiresPTY(t *testing.T) {
	f := newFake("shell")
	f.hasTTY = false
	if got := Main(f.deps()); got != 1 {
		t.Errorf("exit = %d, want 1", got)
	}
	if len(f.execCalls) != 0 {
		t.Error("exec must not run without a PTY")
	}
}

func TestKeepsAgentCommandsRestricted(t *testing.T) {
	f := newFake("sshdesk-agent info; id")
	Main(f.deps())
	if len(f.agentHits) != 1 || f.agentHits[0][0] != "sshdesk-agent info; id" {
		t.Errorf("agent hits = %v, want the raw command forwarded", f.agentHits)
	}
	if len(f.execCalls) != 0 {
		t.Error("agent grammar must never exec a shell")
	}
}

func TestExplicitDesktopCommandRunsServer(t *testing.T) {
	for _, command := range []string{"desktop", "sshdesk", "sshdesk-server"} {
		f := newFake(command)
		if got := Main(f.deps()); got != 0 {
			t.Errorf("%s: exit = %d, want 0", command, got)
		}
		if f.serverHits != 1 {
			t.Errorf("%s: server hits = %d, want 1", command, f.serverHits)
		}
	}
}

func TestDesktopCommandRequiresPTY(t *testing.T) {
	f := newFake("sshdesk")
	f.hasTTY = false
	if got := Main(f.deps()); got != 1 {
		t.Errorf("exit = %d, want 1", got)
	}
	if f.serverHits != 0 {
		t.Error("server must not start without a PTY")
	}
	if want := "SSHDESK requires an interactive SSH terminal (PTY)."; !bytes.Contains(f.stderr.Bytes(), []byte(want)) {
		t.Errorf("stderr = %q, want %q", &f.stderr, want)
	}
}

func TestNoCommandRunsServer(t *testing.T) {
	f := newFake("")
	if got := Main(f.deps()); got != 0 {
		t.Errorf("exit = %d, want 0", got)
	}
	if f.serverHits != 1 {
		t.Errorf("server hits = %d, want 1", f.serverHits)
	}
}

func TestAgentCommandBasenameRoutesToAllowlist(t *testing.T) {
	f := newFake("/usr/local/bin/sshdesk-agent info")
	Main(f.deps())
	if len(f.agentHits) != 1 {
		t.Errorf("agent hits = %v", f.agentHits)
	}
}

func TestRunAsElevationUsesSudoVectors(t *testing.T) {
	f := newFake("sshdesk")
	f.config["RUN_AS"] = "desktop-user"
	if got := Main(f.deps()); got != 0 {
		t.Fatalf("exit = %d", got)
	}
	want := []string{"/usr/bin/sudo", "-n", "-u", "desktop-user", "--", "/usr/local/bin/sshdesk", "server"}
	if len(f.execCalls) != 1 || !reflect.DeepEqual(f.execCalls[0], want) {
		t.Errorf("exec calls = %v, want [%v]", f.execCalls, want)
	}
	if f.serverHits != 0 {
		t.Error("elevated desktop must exec sudo, not run the server inline")
	}
}

func TestRunAsElevationForAgent(t *testing.T) {
	f := newFake("sshdesk-agent info")
	f.config["RUN_AS"] = "desktop-user"
	Main(f.deps())
	want := []string{"/usr/bin/sudo", "-n", "-u", "desktop-user", "--", "/usr/local/bin/sshdesk", "agent-ssh", "sshdesk-agent info"}
	if len(f.execCalls) != 1 || !reflect.DeepEqual(f.execCalls[0], want) {
		t.Errorf("exec calls = %v, want [%v]", f.execCalls, want)
	}
}

func TestShellSelectorNeverElevates(t *testing.T) {
	f := newFake("shell")
	f.config["RUN_AS"] = "desktop-user"
	Main(f.deps())
	if len(f.execCalls) != 1 || f.execCalls[0][0] == "/usr/bin/sudo" {
		t.Errorf("shell selector must never sudo: %v", f.execCalls)
	}
}

func TestInvalidRunAsFails(t *testing.T) {
	f := newFake("sshdesk")
	f.configErr = io.ErrUnexpectedEOF
	if got := Main(f.deps()); got != 1 {
		t.Errorf("exit = %d, want 1", got)
	}
	if want := "Invalid SSHDESK desktop account."; !bytes.Contains(f.stderr.Bytes(), []byte(want)) {
		t.Errorf("stderr = %q, want %q", &f.stderr, want)
	}
}

func TestUnterminatedQuoteFallsToAgentRoute(t *testing.T) {
	f := newFake("sshdesk-agent 'broken")
	Main(f.deps())
	if len(f.agentHits) != 1 {
		t.Errorf("agent hits = %v, want the raw command forwarded to the allowlist", f.agentHits)
	}
}
