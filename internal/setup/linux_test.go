package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// linuxFixture points every system path at a temporary root and fakes the
// remaining seams.
type linuxFixture struct {
	paths   linuxPaths
	deps    Deps
	stdout  *strings.Builder
	runs    []string
	failRun func(name string, args ...string) bool
	steps   []string
	t       *testing.T
}

func newLinuxFixture(t *testing.T) *linuxFixture {
	t.Helper()
	root := t.TempDir()
	f := &linuxFixture{t: t, stdout: &strings.Builder{}}
	f.paths = linuxPaths{
		binDir:         filepath.Join(root, "usr", "local", "bin"),
		configDir:      filepath.Join(root, "etc", "sshdesk"),
		sudoersDir:     filepath.Join(root, "etc", "sudoers.d"),
		sshdConfig:     filepath.Join(root, "etc", "ssh", "sshd_config"),
		sshdConfigDir:  filepath.Join(root, "etc", "ssh", "sshd_config.d"),
		systemdDir:     filepath.Join(root, "etc", "systemd", "system"),
		modulesLoadDir: filepath.Join(root, "etc", "modules-load.d"),
		libexecDir:     filepath.Join(root, "usr", "local", "libexec", "sshdesk"),
		ydotoolCLI:     filepath.Join(root, "usr", "local", "bin", "ydotool"),
	}
	if err := os.MkdirAll(filepath.Dir(f.paths.sshdConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.paths.sshdConfig, []byte("Port 22\nPasswordAuthentication no\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	self := filepath.Join(root, "self", "sshdesk")
	if err := os.MkdirAll(filepath.Dir(self), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(self, []byte("binary-payload"), 0o755); err != nil {
		t.Fatal(err)
	}

	d, _, stderr := fakeDeps()
	d.Stdout = f.stdout
	d.Stderr = f.stdout
	_ = stderr
	d.Getenv = envMap("USER", "alice", "DISPLAY", ":0")
	d.LookupUser = func(name string) (string, int, int, error) {
		if name == "alice" {
			return "/home/alice", 1000, 1001, nil
		}
		return "", 0, 0, fmt.Errorf("unknown user: %s", name)
	}
	d.SelfPath = func() (string, error) { return self, nil }
	d.LookPath = func(name string) (string, error) {
		switch name {
		case "sshd", "systemctl", "visudo":
			return "/usr/sbin/" + name, nil
		}
		return "", errors.New("not found: " + name)
	}
	d.Run = func(name string, args ...string) error {
		f.runs = append(f.runs, name+" "+strings.Join(args, " "))
		if f.failRun != nil && f.failRun(name, args...) {
			return errors.New("command failed")
		}
		return nil
	}
	d.OnStep = func(name string) { f.steps = append(f.steps, name) }
	f.deps = d
	return f
}

func (f *linuxFixture) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestLinuxInstallAndUninstall(t *testing.T) {
	f := newLinuxFixture(t)
	p := f.paths

	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, p)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	wantSteps := []string{
		"install-binary", "write-config", "write-sudoers", "configure-sshd",
		"reload-openssh", "setup-ydotoold", "check-dependencies", "verify-access",
	}
	if strings.Join(f.steps, " ") != strings.Join(wantSteps, " ") {
		t.Fatalf("steps = %v, want %v", f.steps, wantSteps)
	}

	// The binary and its eight symlinks are installed.
	if got := f.read(t, p.binPath()); got != "binary-payload" {
		t.Errorf("installed binary = %q", got)
	}
	for _, name := range CommandNames[1:] {
		target, err := os.Readlink(filepath.Join(p.binDir, name))
		if err != nil || target != p.binPath() {
			t.Errorf("symlink %s -> %q, %v", name, target, err)
		}
	}

	// The per-account config matches the former install-server.sh output.
	wantConfig := `DISPLAY=:0
XAUTHORITY=/home/alice/.Xauthority
RUN_AS=alice
SSHDESK_RENDER=auto
SSHDESK_COLOR=auto
SSHDESK_MOUSE=auto
SSHDESK_UNICODE=auto
SSHDESK_X11_CAPTURE=auto
SSHDESK_MAX_FPS=auto
SSHDESK_SCALE=1.0
`
	if got := f.read(t, p.accountConfig("alice")); got != wantConfig {
		t.Errorf("config =\n%s\nwant:\n%s", got, wantConfig)
	}
	info, err := os.Stat(p.accountConfig("alice"))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("config mode = %v, %v", info.Mode(), err)
	}

	// RUN_AS == account: no sudoers rule is written.
	if fileExists(p.sudoers("alice")) {
		t.Error("no sudoers rule expected when RUN_AS equals the account")
	}

	// The Include line was prepended once, with a backup, and the snippet is
	// the forced-command Match block.
	main := f.read(t, p.sshdConfig)
	if !strings.HasPrefix(main, "Include /etc/ssh/sshd_config.d/*.conf\nPort 22\n") {
		t.Errorf("sshd_config missing the Include line:\n%s", main)
	}
	if got := f.read(t, p.sshdConfig+".before-sshdesk"); got != "Port 22\nPasswordAuthentication no\n" {
		t.Errorf("backup = %q", got)
	}
	wantSnippet := RenderSshdSnippet("alice", p.binDir+"/sshdesk-forced-command")
	if got := f.read(t, p.snippet("alice")); got != wantSnippet {
		t.Errorf("snippet =\n%s\nwant:\n%s", got, wantSnippet)
	}

	// sshd -t ran and OpenSSH was enabled and reloaded.
	joined := strings.Join(f.runs, "\n")
	if !strings.Contains(joined, "sshd -t") {
		t.Errorf("sshd -t did not run:\n%s", joined)
	}
	if !strings.Contains(joined, "systemctl enable --now ssh.service") ||
		!strings.Contains(joined, "systemctl reload ssh.service") {
		t.Errorf("OpenSSH was not enabled and reloaded:\n%s", joined)
	}
	// The desktop access check ran as the account with the session environment.
	if !strings.Contains(joined, "sudo -n -u alice env DISPLAY=:0 XAUTHORITY=/home/alice/.Xauthority "+p.binPath()+" server --check") {
		t.Errorf("verify-access did not run:\n%s", joined)
	}

	// Reinstalling is idempotent.
	f.steps = nil
	if code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, p); code != 0 {
		t.Fatalf("reinstall exit = %d", code)
	}
	if got := f.read(t, p.sshdConfig); got != main {
		t.Errorf("reinstall rewrote sshd_config:\n%s", got)
	}

	// Plant a foreign file under a command name; the uninstaller must keep it.
	foreign := filepath.Join(p.binDir, "sshdesk-bench")
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("not ours"), 0o755); err != nil {
		t.Fatal(err)
	}

	f.steps = nil
	f.runs = nil
	f.stdout.Reset()
	code = linuxUninstall(f.deps, UninstallOptions{User: "alice", Yes: true}, p)
	if code != 0 {
		t.Fatalf("uninstall exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	wantSteps = []string{
		"remove-sshd-snippet", "validate-sshd", "reload-openssh",
		"remove-sudoers", "remove-config", "remove-binaries", "remove-ydotoold",
	}
	if strings.Join(f.steps, " ") != strings.Join(wantSteps, " ") {
		t.Fatalf("uninstall steps = %v, want %v", f.steps, wantSteps)
	}
	for _, name := range CommandNames {
		path := filepath.Join(p.binDir, name)
		if name == "sshdesk-bench" {
			if got := f.read(t, path); got != "not ours" {
				t.Errorf("the foreign file was modified: %q", got)
			}
			continue
		}
		if fileExists(path) {
			t.Errorf("%s was not removed", path)
		}
	}
	if !strings.Contains(f.stdout.String(), "Keeping "+foreign) {
		t.Errorf("missing keep note:\n%s", f.stdout.String())
	}
	if fileExists(p.snippet("alice")) || fileExists(p.accountConfig("alice")) {
		t.Error("the snippet and config must be removed")
	}
	if fileExists(p.configDir) {
		t.Error("the empty config directory must be removed")
	}
	if !fileExists(p.sshdConfig) {
		t.Error("sshd_config itself must be left untouched")
	}
}

func TestLinuxInstallRequiresRoot(t *testing.T) {
	f := newLinuxFixture(t)
	f.deps.Getuid = func() int { return 1000 }
	if code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths); code != 1 {
		t.Fatalf("non-root install exit = %d, want 1", code)
	}
	if code := linuxUninstall(f.deps, UninstallOptions{User: "alice", Yes: true}, f.paths); code != 1 {
		t.Fatalf("non-root uninstall exit = %d, want 1", code)
	}
}

func TestLinuxInstallRejectsBadInput(t *testing.T) {
	f := newLinuxFixture(t)
	if code := linuxInstall(f.deps, InstallOptions{User: "al ice", Yes: true}, f.paths); code != 2 {
		t.Errorf("invalid account exit = %d, want 2", code)
	}
	if code := linuxInstall(f.deps, InstallOptions{User: "nobody", Yes: true}, f.paths); code != 1 {
		t.Errorf("unknown account exit = %d, want 1", code)
	}
	if code := linuxInstall(f.deps, InstallOptions{User: "alice", Display: ":0\nevil", Yes: true}, f.paths); code != 2 {
		t.Errorf("newline display exit = %d, want 2", code)
	}
	if code := linuxInstall(f.deps, InstallOptions{User: "alice", RunAs: "nobody", Yes: true}, f.paths); code != 1 {
		t.Errorf("unknown run-as exit = %d, want 1", code)
	}
}

func TestLinuxInstallSudoersForSeparateRunAs(t *testing.T) {
	f := newLinuxFixture(t)
	f.deps.LookupUser = func(name string) (string, int, int, error) {
		switch name {
		case "alice":
			return "/home/alice", 1000, 1001, nil
		case "bob":
			return "/home/bob", 1002, 1003, nil
		}
		return "", 0, 0, fmt.Errorf("unknown user: %s", name)
	}
	code := linuxInstall(f.deps, InstallOptions{User: "alice", RunAs: "bob", Yes: true}, f.paths)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	want := RenderSudoers("alice", "bob", f.paths.binPath())
	if got := f.read(t, f.paths.sudoers("alice")); got != want {
		t.Errorf("sudoers =\n%s\nwant:\n%s", got, want)
	}
	info, err := os.Stat(f.paths.sudoers("alice"))
	if err != nil || info.Mode().Perm() != 0o440 {
		t.Errorf("sudoers mode = %v, %v", info.Mode(), err)
	}
	joined := strings.Join(f.runs, "\n")
	if !strings.Contains(joined, "visudo -cf "+f.paths.sudoers("alice")) {
		t.Errorf("visudo did not validate the rule:\n%s", joined)
	}
	if !strings.Contains(joined, "sudo -n -u bob env") {
		t.Errorf("the access check did not run as bob:\n%s", joined)
	}
}

func TestLinuxInstallSshdFailureRollsBack(t *testing.T) {
	f := newLinuxFixture(t)
	f.failRun = func(name string, args ...string) bool {
		return strings.HasSuffix(name, "sshd") && len(args) == 1 && args[0] == "-t"
	}
	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths)
	if code != 1 {
		t.Fatalf("install exit = %d, want 1", code)
	}
	if fileExists(f.paths.snippet("alice")) {
		t.Error("the rejected snippet must be rolled back")
	}
	if got := f.read(t, f.paths.sshdConfig); got != "Port 22\nPasswordAuthentication no\n" {
		t.Errorf("sshd_config was not restored:\n%s", got)
	}
}

func TestLinuxInstallWaylandSetsUpYdotoold(t *testing.T) {
	f := newLinuxFixture(t)
	f.deps.Getenv = envMap(
		"USER", "alice", "DISPLAY", ":0",
		"XDG_SESSION_TYPE", "wayland", "XDG_CURRENT_DESKTOP", "KDE",
		"WAYLAND_DISPLAY", "wayland-0",
	)
	// The pinned helper binaries exist; /dev/uinput is absent on this host,
	// which the modprobe branch tolerates only with modprobe present.
	p := f.paths
	if err := os.MkdirAll(p.libexecDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.libexecDir, "ydotoold"), []byte("daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.ydotoolCLI, []byte("cli"), 0o755); err != nil {
		t.Fatal(err)
	}
	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, p)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	if fileExists("/dev/uinput") {
		unit := filepath.Join(p.systemdDir, "sshdesk-ydotoold.service")
		want := RenderYdotoolUnit(filepath.Join(p.libexecDir, "ydotoold"),
			"/run/sshdesk-ydotool/socket", 1000, 1001)
		if got := f.read(t, unit); got != want {
			t.Errorf("unit =\n%s\nwant:\n%s", got, want)
		}
		if got := f.read(t, filepath.Join(p.modulesLoadDir, "sshdesk-uinput.conf")); got != "uinput\n" {
			t.Errorf("modules-load conf = %q", got)
		}
		joined := strings.Join(f.runs, "\n")
		if !strings.Contains(joined, "systemctl enable --now sshdesk-ydotoold.service") {
			t.Errorf("ydotoold was not enabled:\n%s", joined)
		}
	} else {
		// Without /dev/uinput and without modprobe the step warns and moves on.
		if !strings.Contains(f.stdout.String(), "modprobe") {
			t.Errorf("expected a modprobe warning:\n%s", f.stdout.String())
		}
	}
	// The uninstaller removes the helper only when the unit exists.
	unit := filepath.Join(p.systemdDir, "sshdesk-ydotoold.service")
	if fileExists(unit) {
		f.steps = nil
		if code := linuxUninstall(f.deps, UninstallOptions{User: "alice", Yes: true}, p); code != 0 {
			t.Fatalf("uninstall exit = %d", code)
		}
		if fileExists(unit) || fileExists(filepath.Join(p.libexecDir, "ydotoold")) || fileExists(p.ydotoolCLI) {
			t.Error("the ydotool helper must be removed")
		}
	}
}

func TestLinuxInstallWarnsAboutMissingCaptureTools(t *testing.T) {
	f := newLinuxFixture(t)
	f.deps.Getenv = envMap("USER", "alice", "DISPLAY", ":0")
	f.deps.LookPath = func(name string) (string, error) {
		switch name {
		case "sshd", "systemctl", "apt-get":
			return "/usr/sbin/" + name, nil
		}
		return "", errors.New("not found: " + name)
	}
	if code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths); code != 0 {
		t.Fatalf("install exit = %d", code)
	}
	if !strings.Contains(f.stdout.String(), "ffmpeg not found") {
		t.Errorf("expected an ffmpeg note:\n%s", f.stdout.String())
	}
	if !strings.Contains(f.stdout.String(), "apt-get install ffmpeg") {
		t.Errorf("expected an apt suggestion:\n%s", f.stdout.String())
	}
}

// configValues parses KEY=VALUE lines from an account config.
func configValues(t *testing.T, content string) map[string]string {
	t.Helper()
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(content, "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed config line: %q", line)
		}
		values[key] = value
	}
	return values
}

func TestLinuxInstallHarvestsGraphicalSession(t *testing.T) {
	f := newLinuxFixture(t)
	// Plain sudo without --preserve-env: the process carries no session
	// variables, so everything graphical must come from the /proc harvest.
	f.deps.Getenv = envMap("USER", "alice")
	gotUID := -1
	f.deps.HarvestSession = func(uid int) map[string]string {
		gotUID = uid
		return map[string]string{
			"WAYLAND_DISPLAY":          "wayland-0",
			"XDG_RUNTIME_DIR":          "/run/user/1000",
			"XDG_SESSION_TYPE":         "wayland",
			"XDG_CURRENT_DESKTOP":      "GNOME",
			"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
		}
	}
	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	if gotUID != 1000 {
		t.Errorf("HarvestSession uid = %d, want 1000", gotUID)
	}
	values := configValues(t, f.read(t, f.paths.accountConfig("alice")))
	for key, want := range map[string]string{
		"WAYLAND_DISPLAY":          "wayland-0",
		"XDG_RUNTIME_DIR":          "/run/user/1000",
		"XDG_SESSION_TYPE":         "wayland",
		"XDG_CURRENT_DESKTOP":      "GNOME",
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
		"RUN_AS":                   "alice",
	} {
		if values[key] != want {
			t.Errorf("config %s = %q, want %q", key, values[key], want)
		}
	}
	if !strings.Contains(f.stdout.String(),
		"Detected a GNOME Wayland session for user alice") {
		t.Errorf("missing detection note:\n%s", f.stdout.String())
	}
	// The session family comes from the harvest: GNOME triggers the PipeWire
	// dependency note, not the ffmpeg one.
	if !strings.Contains(f.stdout.String(), "GNOME PipeWire capture needs") {
		t.Errorf("expected the GNOME dependency note:\n%s", f.stdout.String())
	}
	// The harvested environment reaches the desktop access check.
	joined := strings.Join(f.runs, "\n")
	if !strings.Contains(joined, "sudo -n -u alice env DISPLAY= XAUTHORITY=/home/alice/.Xauthority WAYLAND_DISPLAY=wayland-0 XDG_RUNTIME_DIR=/run/user/1000") {
		t.Errorf("verify-access missed the harvested environment:\n%s", joined)
	}
}

func TestLinuxInstallSessionVariablePriority(t *testing.T) {
	waylandHarvest := map[string]string{
		"WAYLAND_DISPLAY":     "wayland-0",
		"XDG_RUNTIME_DIR":     "/run/user/1000",
		"XDG_SESSION_TYPE":    "wayland",
		"XDG_CURRENT_DESKTOP": "sway",
	}
	tests := []struct {
		name           string
		display        string
		xauthority     string
		env            func(string) string
		harvest        map[string]string
		wantDisplay    string
		wantXauthority string
	}{
		{"flag beats env and harvest", ":9", "/tmp/flag-auth",
			envMap("USER", "alice", "DISPLAY", ":1", "XAUTHORITY", "/tmp/env-auth"),
			map[string]string{"DISPLAY": ":2", "XAUTHORITY": "/tmp/harvest-auth"},
			":9", "/tmp/flag-auth"},
		{"env beats harvest", "", "",
			envMap("USER", "alice", "DISPLAY", ":1", "XAUTHORITY", "/tmp/env-auth"),
			map[string]string{"DISPLAY": ":2", "XAUTHORITY": "/tmp/harvest-auth"},
			":1", "/tmp/env-auth"},
		{"harvest beats defaults", "", "",
			envMap("USER", "alice"),
			map[string]string{"DISPLAY": ":2", "XAUTHORITY": "/tmp/harvest-auth"},
			":2", "/tmp/harvest-auth"},
		{"wayland harvest suppresses the :0 default", "", "",
			envMap("USER", "alice"), waylandHarvest, "", "/home/alice/.Xauthority"},
		{"defaults without any session", "", "",
			envMap("USER", "alice"), nil, ":0", "/home/alice/.Xauthority"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := newLinuxFixture(t)
			f.deps.Getenv = test.env
			if test.harvest != nil {
				f.deps.HarvestSession = func(int) map[string]string {
					return test.harvest
				}
			}
			opts := InstallOptions{User: "alice", Yes: true,
				Display: test.display, XAuthority: test.xauthority}
			if code := linuxInstall(f.deps, opts, f.paths); code != 0 {
				t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
			}
			values := configValues(t, f.read(t, f.paths.accountConfig("alice")))
			if values["DISPLAY"] != test.wantDisplay {
				t.Errorf("DISPLAY = %q, want %q", values["DISPLAY"], test.wantDisplay)
			}
			if values["XAUTHORITY"] != test.wantXauthority {
				t.Errorf("XAUTHORITY = %q, want %q", values["XAUTHORITY"], test.wantXauthority)
			}
		})
	}
}

func TestLinuxInstallHarvestedKDETriggersYdotooldNote(t *testing.T) {
	f := newLinuxFixture(t)
	f.deps.Getenv = envMap("USER", "alice")
	f.deps.HarvestSession = func(int) map[string]string {
		return map[string]string{
			"WAYLAND_DISPLAY":     "wayland-0",
			"XDG_RUNTIME_DIR":     "/run/user/1000",
			"XDG_SESSION_TYPE":    "wayland",
			"XDG_CURRENT_DESKTOP": "KDE",
		}
	}
	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	output := f.stdout.String()
	// KDE is a ydotool family: the helper note and the capture suggestion
	// prove the family decision used the harvested session.
	if !strings.Contains(output, "ydotool") {
		t.Errorf("expected the ydotool helper note:\n%s", output)
	}
	if !strings.Contains(output, "spectacle not found") {
		t.Errorf("expected the KDE capture note:\n%s", output)
	}
	if !strings.Contains(output, "Detected a KDE Wayland session for user alice") {
		t.Errorf("missing detection note:\n%s", output)
	}
}

func TestLinuxInstallHeadlessStillWritesConfigAndWarns(t *testing.T) {
	f := newLinuxFixture(t)
	// No DISPLAY, no Wayland variables, no harvest: a headless server.
	f.deps.Getenv = envMap("USER", "alice")
	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	// The config still gets the X11 defaults and no Wayland keys.
	values := configValues(t, f.read(t, f.paths.accountConfig("alice")))
	if values["DISPLAY"] != ":0" || values["XAUTHORITY"] != "/home/alice/.Xauthority" {
		t.Errorf("headless defaults wrong: %v", values)
	}
	for _, key := range waylandKeys {
		if _, ok := values[key]; ok {
			t.Errorf("headless config must not contain %s: %v", key, values)
		}
	}
	if values["SSHDESK_SCALE"] != "1.0" {
		t.Errorf("SSHDESK_SCALE = %q, want 1.0", values["SSHDESK_SCALE"])
	}
	// The access check is skipped (it could only fail), and the install ends
	// with a prominent warning plus recovery instructions.
	joined := strings.Join(f.runs, "\n")
	if strings.Contains(joined, "server --check") {
		t.Errorf("the access check must be skipped when headless:\n%s", joined)
	}
	output := f.stdout.String()
	for _, fragment := range []string{
		"WARNING: no graphical session was detected for user alice.",
		"sudo sshdesk --install",
		f.paths.accountConfig("alice"),
		"skipping the desktop access check",
	} {
		if !strings.Contains(output, fragment) {
			t.Errorf("warning missing %q:\n%s", fragment, output)
		}
	}
}

func TestLinuxInstallX11SessionWritesNoWaylandKeys(t *testing.T) {
	f := newLinuxFixture(t)
	// A live X11 session: DISPLAY survives in the process environment.
	f.deps.Getenv = envMap("USER", "alice", "DISPLAY", ":1",
		"XAUTHORITY", "/home/alice/.Xauthority")
	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	values := configValues(t, f.read(t, f.paths.accountConfig("alice")))
	if values["DISPLAY"] != ":1" {
		t.Errorf("DISPLAY = %q, want :1", values["DISPLAY"])
	}
	for _, key := range waylandKeys {
		if _, ok := values[key]; ok {
			t.Errorf("X11 config must not contain %s: %v", key, values)
		}
	}
	// A detected session runs the access check with exactly the config
	// environment.
	joined := strings.Join(f.runs, "\n")
	if !strings.Contains(joined,
		"sudo -n -u alice env DISPLAY=:1 XAUTHORITY=/home/alice/.Xauthority "+
			f.paths.binPath()+" server --check") {
		t.Errorf("verify-access did not run with the config environment:\n%s", joined)
	}
	if strings.Contains(f.stdout.String(), "no graphical session was detected") {
		t.Errorf("an X11 session must not trigger the headless warning:\n%s", f.stdout.String())
	}
}

func TestLinuxInstallVerifyEnvMatchesConfig(t *testing.T) {
	f := newLinuxFixture(t)
	f.deps.Getenv = envMap("USER", "alice")
	f.deps.HarvestSession = func(int) map[string]string {
		return map[string]string{
			"WAYLAND_DISPLAY":     "wayland-0",
			"XDG_RUNTIME_DIR":     "/run/user/1000",
			"XDG_SESSION_TYPE":    "wayland",
			"XDG_CURRENT_DESKTOP": "sway",
			"DISPLAY":             ":3",
			"XAUTHORITY":          "/run/user/1000/.mutter-Xwaylandauth.XYZ",
		}
	}
	code := linuxInstall(f.deps, InstallOptions{User: "alice", Yes: true}, f.paths)
	if code != 0 {
		t.Fatalf("install exit = %d\noutput:\n%s", code, f.stdout.String())
	}
	values := configValues(t, f.read(t, f.paths.accountConfig("alice")))
	// The harvested values reach both the config and the check unchanged.
	if values["DISPLAY"] != ":3" ||
		values["XAUTHORITY"] != "/run/user/1000/.mutter-Xwaylandauth.XYZ" {
		t.Errorf("harvested DISPLAY/XAUTHORITY not recorded: %v", values)
	}
	joined := strings.Join(f.runs, "\n")
	want := "sudo -n -u alice env DISPLAY=" + values["DISPLAY"] +
		" XAUTHORITY=" + values["XAUTHORITY"] +
		" WAYLAND_DISPLAY=" + values["WAYLAND_DISPLAY"] +
		" XDG_RUNTIME_DIR=" + values["XDG_RUNTIME_DIR"] +
		" XDG_SESSION_TYPE=" + values["XDG_SESSION_TYPE"] +
		" XDG_CURRENT_DESKTOP=" + values["XDG_CURRENT_DESKTOP"] +
		" " + f.paths.binPath() + " server --check"
	if !strings.Contains(joined, want) {
		t.Errorf("verify-access environment diverges from the config:\nwant: %s\nruns:\n%s", want, joined)
	}
}

func TestSSHServiceUnits(t *testing.T) {
	// Without the quiet seam the legacy Debian-first order is kept.
	if got := sshServiceUnits(Deps{}); got[0] != "ssh.service" || len(got) != 2 {
		t.Errorf("legacy order = %v", got)
	}
	// A missing ssh.service (Arch/Fedora) is probed quietly and skipped.
	d := Deps{RunQuiet: func(name string, args ...string) error {
		if len(args) == 2 && args[0] == "cat" && args[1] == "ssh.service" {
			return errors.New("unit not found")
		}
		return nil
	}}
	got := sshServiceUnits(d)
	if len(got) != 1 || got[0] != "sshd.service" {
		t.Errorf("probed units = %v, want [sshd.service]", got)
	}
	// When every probe fails the legacy attempts are kept as a fallback.
	d.RunQuiet = func(string, ...string) error { return errors.New("no systemd") }
	if got := sshServiceUnits(d); got[0] != "ssh.service" || len(got) != 2 {
		t.Errorf("fallback order = %v", got)
	}
}

func TestReloadOpenSSHPicksExistingUnit(t *testing.T) {
	f := newLinuxFixture(t)
	var quiet []string
	f.deps.RunQuiet = func(name string, args ...string) error {
		quiet = append(quiet, name+" "+strings.Join(args, " "))
		if len(args) == 2 && args[0] == "cat" && args[1] == "ssh.service" {
			return errors.New("Unit ssh.service does not exist")
		}
		return nil
	}
	if err := reloadOpenSSH(f.deps); err != nil {
		t.Fatalf("reloadOpenSSH = %v", err)
	}
	joined := strings.Join(f.runs, "\n")
	if strings.Contains(joined, "ssh.service") {
		t.Errorf("the missing unit must not be touched:\n%s", joined)
	}
	if !strings.Contains(joined, "systemctl enable --now sshd.service") ||
		!strings.Contains(joined, "systemctl reload sshd.service") {
		t.Errorf("sshd.service was not enabled and reloaded:\n%s", joined)
	}
	if len(quiet) == 0 {
		t.Error("units were not probed quietly")
	}
}
