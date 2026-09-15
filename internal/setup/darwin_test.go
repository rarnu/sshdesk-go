package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// darwinFixture fakes a macOS home directory and system sshd paths inside a
// temporary root.
type darwinFixture struct {
	paths  darwinPaths
	deps   Deps
	stdout *strings.Builder
	runs   []string
	steps  []string
	t      *testing.T
}

func newDarwinFixture(t *testing.T) *darwinFixture {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "Users", "alice")
	f := &darwinFixture{t: t, stdout: &strings.Builder{}}
	f.paths = defaultDarwinPaths(home)
	f.paths.sshdConfig = filepath.Join(root, "etc", "ssh", "sshd_config")
	f.paths.sshdConfigDir = filepath.Join(root, "etc", "ssh", "sshd_config.d")
	if err := os.MkdirAll(filepath.Dir(f.paths.sshdConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.paths.sshdConfig, []byte("Port 22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(root, "Downloads", "sshdesk")
	if err := os.MkdirAll(filepath.Dir(self), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(self, []byte("binary-payload"), 0o755); err != nil {
		t.Fatal(err)
	}

	d, _, _ := fakeDeps()
	d.Stdout = f.stdout
	d.Stderr = f.stdout
	d.Getuid = func() int { return 1000 }
	d.Getenv = envMap("USER", "alice")
	d.LookupUser = func(name string) (string, int, int, error) {
		if name == "alice" {
			return home, 501, 20, nil
		}
		return "", 0, 0, fmt.Errorf("unknown user: %s", name)
	}
	d.SelfPath = func() (string, error) { return self, nil }
	d.LookPath = func(name string) (string, error) {
		if name == "sshd" {
			return "/usr/sbin/sshd", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	}
	d.Run = func(name string, args ...string) error {
		f.runs = append(f.runs, name+" "+strings.Join(args, " "))
		return nil
	}
	d.OnStep = func(name string) { f.steps = append(f.steps, name) }
	f.deps = d
	return f
}

func TestDarwinUserLevelInstallAndUninstall(t *testing.T) {
	f := newDarwinFixture(t)
	p := f.paths

	code := darwinInstall(f.deps, InstallOptions{User: "alice", Yes: true}, p)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	if strings.Join(f.steps, " ") != "install-binary" {
		t.Fatalf("steps = %v, want [install-binary]", f.steps)
	}
	if data, err := os.ReadFile(p.binPath()); err != nil || string(data) != "binary-payload" {
		t.Fatalf("installed binary = %q, %v", data, err)
	}
	// All nine command names are symlinks into the install root.
	for _, name := range CommandNames {
		target, err := os.Readlink(filepath.Join(p.binDir, name))
		if err != nil || target != p.binPath() {
			t.Errorf("symlink %s -> %q, %v", name, target, err)
		}
	}
	if !strings.Contains(f.stdout.String(), "rerun with: sudo sshdesk --install") {
		t.Errorf("expected the sudo hint:\n%s", f.stdout.String())
	}

	// A foreign plain file under a command name survives the uninstall.
	foreign := filepath.Join(p.binDir, "sshdesk-local")
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("not ours"), 0o755); err != nil {
		t.Fatal(err)
	}

	f.steps = nil
	f.stdout.Reset()
	code = darwinUninstall(f.deps, UninstallOptions{User: "alice", Yes: true}, p)
	if code != 0 {
		t.Fatalf("uninstall exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	if strings.Join(f.steps, " ") != "remove-sshd-snippet remove-binaries" {
		t.Fatalf("uninstall steps = %v", f.steps)
	}
	if fileExists(p.installRoot) {
		t.Error("the install root must be removed")
	}
	for _, name := range CommandNames {
		path := filepath.Join(p.binDir, name)
		if name == "sshdesk-local" {
			if data, _ := os.ReadFile(path); string(data) != "not ours" {
				t.Error("the foreign file was modified")
			}
			continue
		}
		if fileExists(path) {
			t.Errorf("%s was not removed", path)
		}
	}
}

func TestDarwinRootInstallConfiguresSshd(t *testing.T) {
	f := newDarwinFixture(t)
	p := f.paths
	f.deps.Getuid = func() int { return 0 }

	code := darwinInstall(f.deps, InstallOptions{User: "alice", Yes: true}, p)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	wantSteps := []string{"install-binary", "configure-sshd", "enable-remote-login"}
	if strings.Join(f.steps, " ") != strings.Join(wantSteps, " ") {
		t.Fatalf("steps = %v, want %v", f.steps, wantSteps)
	}
	main := mustRead(t, p.sshdConfig)
	if !strings.HasPrefix(main, "Include /etc/ssh/sshd_config.d/*\nPort 22\n") {
		t.Errorf("sshd_config missing the Include line:\n%s", main)
	}
	wantSnippet := RenderSshdSnippet("alice", filepath.Join(p.binDir, "sshdesk-forced-command"))
	if got := mustRead(t, p.snippet("alice")); got != wantSnippet {
		t.Errorf("snippet =\n%s\nwant:\n%s", got, wantSnippet)
	}
	joined := strings.Join(f.runs, "\n")
	if !strings.Contains(joined, "systemsetup -setremotelogin on") {
		t.Errorf("Remote Login was not enabled:\n%s", joined)
	}

	// The root uninstall removes the snippet and validates sshd afterwards.
	f.runs = nil
	if code := darwinUninstall(f.deps, UninstallOptions{User: "alice", Yes: true}, p); code != 0 {
		t.Fatalf("uninstall exit = %d", code)
	}
	if fileExists(p.snippet("alice")) {
		t.Error("the snippet must be removed")
	}
	joined = strings.Join(f.runs, "\n")
	if !strings.Contains(joined, "sshd -t") {
		t.Errorf("sshd was not validated after removal:\n%s", joined)
	}
}

func TestDarwinUserMismatchRejected(t *testing.T) {
	f := newDarwinFixture(t)
	f.deps.CurrentUser = func() (string, error) { return "mallory", nil }
	if code := darwinInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths); code != 1 {
		t.Errorf("install as another user exit = %d, want 1", code)
	}
	if code := darwinUninstall(f.deps, UninstallOptions{User: "alice", Yes: true}, f.paths); code != 1 {
		t.Errorf("uninstall as another user exit = %d, want 1", code)
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
