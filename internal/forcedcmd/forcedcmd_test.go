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

// wantCommandShell is the expected exec vector for a remote command passed
// verbatim to the account's login shell.
func (f *fakeDeps) wantCommandShell(command string) []string {
	if isWindows {
		return []string{f.shell, "/c", command}
	}
	return []string{f.shell, "-c", command}
}

func (f *fakeDeps) wantLoginShell() []string {
	if isWindows {
		return []string{f.shell}
	}
	return []string{"-bash"}
}

func TestDesktopSelectorRunsServer(t *testing.T) {
	f := newFake("desktop")
	if got := Main(f.deps()); got != 0 {
		t.Errorf("exit = %d, want 0", got)
	}
	if f.serverHits != 1 {
		t.Errorf("server hits = %d, want 1", f.serverHits)
	}
	if len(f.execCalls) != 0 {
		t.Errorf("desktop without elevation must not exec: %v", f.execCalls)
	}
}

func TestDesktopSelectorRequiresPTY(t *testing.T) {
	f := newFake("desktop")
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

func TestNoCommandRunsLoginShell(t *testing.T) {
	f := newFake("")
	if got := Main(f.deps()); got != 0 {
		t.Errorf("exit = %d, want 0 (stderr %s)", got, &f.stderr)
	}
	if len(f.execCalls) != 1 || !reflect.DeepEqual(f.execCalls[0], f.wantLoginShell()) {
		t.Errorf("exec calls = %v, want [%v]", f.execCalls, f.wantLoginShell())
	}
	if f.serverHits != 0 {
		t.Error("no command must not start the desktop")
	}
}

func TestNoCommandShellDoesNotRequirePTY(t *testing.T) {
	f := newFake("")
	f.hasTTY = false
	if got := Main(f.deps()); got != 0 {
		t.Errorf("exit = %d, want 0; standard ssh runs a shell without a PTY", got)
	}
	if len(f.execCalls) != 1 {
		t.Errorf("exec calls = %v, want the login shell", f.execCalls)
	}
}

// TestAnyCommandPassesVerbatimToShellC covers every command that is not the
// exact desktop selector, including the retired special routes, quotes,
// pipes, and redirections: the original text reaches shell -c untouched.
func TestAnyCommandPassesVerbatimToShellC(t *testing.T) {
	commands := []string{
		"id",
		"sshdesk",
		"sshdesk-server",
		"shell",
		"sshdesk-shell",
		"sshdesk-agent info",
		"sshdesk-agent info; id",
		"/usr/local/bin/sshdesk-agent info",
		"echo \"a b\" | grep a > /tmp/x",
		"sshdesk-agent 'broken",
		"desktop --check",
		" desktop",
		"desktop ",
		"DESKTOP",
	}
	for _, command := range commands {
		f := newFake(command)
		if got := Main(f.deps()); got != 0 {
			t.Errorf("%q: exit = %d, want 0 (stderr %s)", command, got, &f.stderr)
		}
		want := f.wantCommandShell(command)
		if len(f.execCalls) != 1 || !reflect.DeepEqual(f.execCalls[0], want) {
			t.Errorf("%q: exec calls = %v, want [%v]", command, f.execCalls, want)
		}
		if f.serverHits != 0 {
			t.Errorf("%q: must not start the desktop", command)
		}
	}
}

func TestRunAsElevationUsesSudoVectors(t *testing.T) {
	f := newFake("desktop")
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

// TestShellPathsNeverElevate pins that only the desktop path may sudo: the
// login shell and any remote command always run as the authenticated account.
func TestShellPathsNeverElevate(t *testing.T) {
	for _, command := range []string{"", "id", "sshdesk-agent info", "shell"} {
		f := newFake(command)
		f.config["RUN_AS"] = "desktop-user"
		if got := Main(f.deps()); got != 0 {
			t.Errorf("%q: exit = %d, want 0", command, got)
		}
		if len(f.execCalls) != 1 || f.execCalls[0][0] == "/usr/bin/sudo" {
			t.Errorf("%q: shell paths must never sudo: %v", command, f.execCalls)
		}
	}
}

func TestInvalidRunAsFailsOnEveryPath(t *testing.T) {
	for _, command := range []string{"desktop", "", "id"} {
		f := newFake(command)
		f.configErr = io.ErrUnexpectedEOF
		if got := Main(f.deps()); got != 1 {
			t.Errorf("%q: exit = %d, want 1", command, got)
		}
		if want := "Invalid SSHDESK desktop account."; !bytes.Contains(f.stderr.Bytes(), []byte(want)) {
			t.Errorf("%q: stderr = %q, want %q", command, &f.stderr, want)
		}
		if f.serverHits != 0 || len(f.execCalls) != 0 {
			t.Errorf("%q: nothing may run with an invalid configuration", command)
		}
	}
}
