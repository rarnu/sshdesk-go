package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeDeps returns Deps with every seam fake; tests override what they need.
func fakeDeps() (Deps, *bytes.Buffer, *bytes.Buffer) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	d := Deps{
		Stdout:  stdout,
		Stderr:  stderr,
		Getenv:  func(string) string { return "" },
		Getuid:  func() int { return 0 },
		IsAdmin: func() bool { return true },
		LookupUser: func(name string) (string, int, int, error) {
			return "", 0, 0, fmt.Errorf("unknown user: %s", name)
		},
		CurrentUser: func() (string, error) { return "alice", nil },
		Logname:     func() (string, error) { return "", errors.New("no logname") },
		SelfPath:    func() (string, error) { return "", errors.New("no self") },
		LookPath:    func(name string) (string, error) { return "", errors.New("not found: " + name) },
		Run:         func(string, ...string) error { return nil },
		Chown:       func(string, int, int) error { return nil },
		Confirm:     func(string) bool { return true },
	}
	return d, stdout, stderr
}

// envMap turns pairs into a Getenv implementation.
func envMap(pairs ...string) func(string) string {
	values := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		values[pairs[i]] = pairs[i+1]
	}
	return func(key string) string { return values[key] }
}

func TestSubcommand(t *testing.T) {
	expected := map[string]string{
		"sshdesk-server":         "server",
		"sshdesk-bench":          "bench",
		"sshdesk-local":          "local",
		"sshdesk-forced-command": "forced-command",
		"sshdesk-agent":          "agent",
		"sshdesk-agent-ssh":      "agent-ssh",
		"sshdesk-split":          "split",
		"sshdesk-remote":         "remote",
	}
	if len(CommandNames) != 9 || CommandNames[0] != "sshdesk" {
		t.Fatalf("unexpected command list: %v", CommandNames)
	}
	for _, name := range CommandNames[1:] {
		if Subcommand(name) != expected[name] {
			t.Errorf("Subcommand(%s) = %q, want %q", name, Subcommand(name), expected[name])
		}
	}
	if Subcommand("sshdesk") != "" {
		t.Errorf("Subcommand(sshdesk) = %q, want empty", Subcommand("sshdesk"))
	}
}

func TestDetectUser(t *testing.T) {
	tests := []struct {
		name    string
		env     func(string) string
		logname func() (string, error)
		want    string
		wantErr bool
	}{
		{"sudo user wins", envMap("SUDO_USER", "bob", "USER", "alice"), nil, "bob", false},
		{"sudo root falls back to USER", envMap("SUDO_USER", "root", "USER", "alice"), nil, "alice", false},
		{"logname fallback", envMap(), func() (string, error) { return "carol", nil }, "carol", false},
		{"root everywhere fails", envMap("USER", "root"), func() (string, error) { return "root", nil }, "", true},
		{"empty fails", envMap(), func() (string, error) { return "", errors.New("x") }, "", true},
		{"invalid characters fail", envMap("USER", "al ice"), nil, "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logname := test.logname
			if logname == nil {
				logname = func() (string, error) { return "", errors.New("no logname") }
			}
			got, err := DetectUser(test.env, logname)
			if test.wantErr {
				if err == nil {
					t.Fatalf("DetectUser() = %q, want error", got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("DetectUser() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestRenderConfig(t *testing.T) {
	env := envMap(
		"WAYLAND_DISPLAY", "wayland-1",
		"XDG_SESSION_TYPE", "wayland",
		"XDG_CURRENT_DESKTOP", "GNOME",
		"DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus",
	)
	want := `DISPLAY=:0
XAUTHORITY=/home/alice/.Xauthority
RUN_AS=alice
WAYLAND_DISPLAY=wayland-1
XDG_SESSION_TYPE=wayland
XDG_CURRENT_DESKTOP=GNOME
DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus
SSHDESK_RENDER=auto
SSHDESK_COLOR=auto
SSHDESK_MOUSE=auto
SSHDESK_UNICODE=auto
SSHDESK_X11_CAPTURE=auto
SSHDESK_MAX_FPS=auto
SSHDESK_SCALE=1.0
`
	if got := RenderConfig(":0", "/home/alice/.Xauthority", "alice", env); got != want {
		t.Errorf("RenderConfig mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderSudoers(t *testing.T) {
	want := `Defaults:alice env_keep += "DISPLAY XAUTHORITY WAYLAND_DISPLAY XDG_RUNTIME_DIR XDG_SESSION_TYPE XDG_CURRENT_DESKTOP DBUS_SESSION_BUS_ADDRESS YDOTOOL_SOCKET SSHDESK_RENDER SSHDESK_COLOR SSHDESK_MOUSE SSHDESK_UNICODE SSHDESK_X11_CAPTURE SSHDESK_MAX_FPS SSHDESK_SCALE TERM"
alice ALL=(alice) NOPASSWD: /usr/local/bin/sshdesk server ""
`
	if got := RenderSudoers("alice", "alice", "/usr/local/bin/sshdesk"); got != want {
		t.Errorf("RenderSudoers mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderSshdSnippet(t *testing.T) {
	want := `# SSHDESK forced command, managed by sshdesk --install.
Match User alice
    ForceCommand /usr/local/bin/sshdesk-forced-command
    PermitTTY yes
    DisableForwarding yes
    X11Forwarding no
    AllowTcpForwarding no
    AllowAgentForwarding no
    PermitTunnel no
    GatewayPorts no
    PermitUserRC no
Match all
`
	if got := RenderSshdSnippet("alice", "/usr/local/bin/sshdesk-forced-command"); got != want {
		t.Errorf("RenderSshdSnippet mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderYdotoolUnit(t *testing.T) {
	want := `[Unit]
Description=SSHDESK Wayland input helper
After=systemd-udevd.service

[Service]
Type=simple
ExecStart=/usr/local/libexec/sshdesk/ydotoold --socket-path=/run/sshdesk-ydotool/socket --socket-perm=0600 --socket-own=1000:1001
Restart=on-failure
RestartSec=1
RuntimeDirectory=sshdesk-ydotool
RuntimeDirectoryMode=0755
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectControlGroups=yes
ProtectKernelLogs=yes
ProtectKernelModules=yes
ProtectKernelTunables=yes
LockPersonality=yes
RestrictSUIDSGID=yes
RestrictAddressFamilies=AF_UNIX
DevicePolicy=closed
DeviceAllow=/dev/uinput rw

[Install]
WantedBy=multi-user.target
`
	got := RenderYdotoolUnit("/usr/local/libexec/sshdesk/ydotoold",
		"/run/sshdesk-ydotool/socket", 1000, 1001)
	if got != want {
		t.Errorf("RenderYdotoolUnit mismatch:\n%s\nwant:\n%s", got, want)
	}
}

func TestWindowsMarkerBlock(t *testing.T) {
	forced := "C:/ProgramData/SSHDESK/bin/sshdesk-forced-command.cmd"
	original := "Port 22\r\nPasswordAuthentication no\r\n"
	updated := ReplaceMarkedBlock(original, "alice", forced)
	wantBlock := "# BEGIN SSHDESK alice\r\n" +
		"Match User alice\r\n" +
		"    ForceCommand " + forced + "\r\n" +
		"    PermitTTY yes\r\n" +
		"    DisableForwarding yes\r\n" +
		"    X11Forwarding no\r\n" +
		"    AllowTcpForwarding no\r\n" +
		"    AllowAgentForwarding no\r\n" +
		"    PermitTunnel no\r\n" +
		"# END SSHDESK alice\r\n"
	if !strings.HasPrefix(updated, "Port 22\r\nPasswordAuthentication no\r\n\r\n") {
		t.Errorf("updated config lost its base content:\n%q", updated)
	}
	if !strings.HasSuffix(updated, wantBlock) {
		t.Errorf("updated config does not end with the marker block:\n%q\nwant suffix:\n%q", updated, wantBlock)
	}
	// Replacing is idempotent and never duplicates the block.
	replaced := ReplaceMarkedBlock(updated, "alice", forced)
	if replaced != updated {
		t.Errorf("ReplaceMarkedBlock is not idempotent:\n%q\nvs\n%q", replaced, updated)
	}
	removed := RemoveMarkedBlock(updated, "alice")
	if removed != "Port 22\r\nPasswordAuthentication no\r\n" {
		t.Errorf("RemoveMarkedBlock = %q", removed)
	}
}

func TestRenderWindowsWrapper(t *testing.T) {
	if got := RenderWindowsWrapper(`C:\SSHDESK\bin\sshdesk.exe`, "server"); got != `@"C:\SSHDESK\bin\sshdesk.exe" server %*`+"\r\n" {
		t.Errorf("wrapper = %q", got)
	}
	if got := RenderWindowsWrapper(`C:\SSHDESK\bin\sshdesk.exe`, ""); got != `@"C:\SSHDESK\bin\sshdesk.exe" %*`+"\r\n" {
		t.Errorf("base wrapper = %q", got)
	}
}

func TestPathEntries(t *testing.T) {
	if got := AddPathEntry(`C:\a;C:\b`, `C:\b`); got != `C:\a;C:\b` {
		t.Errorf("AddPathEntry duplicate = %q", got)
	}
	if got := AddPathEntry(`C:\a;c:\B`, `C:\b`); got != `C:\a;c:\B` {
		t.Errorf("AddPathEntry case-insensitive duplicate = %q", got)
	}
	if got := AddPathEntry("", `C:\a`); got != `C:\a` {
		t.Errorf("AddPathEntry empty = %q", got)
	}
	if got := AddPathEntry(`C:\a`, `C:\b`); got != `C:\a;C:\b` {
		t.Errorf("AddPathEntry append = %q", got)
	}
	if got := RemovePathEntry(`C:\a;C:\b;C:\c`, `c:\B`); got != `C:\a;C:\c` {
		t.Errorf("RemovePathEntry = %q", got)
	}
	if got := RemovePathEntry(`C:\a`, `C:\a`); got != "" {
		t.Errorf("RemovePathEntry last = %q", got)
	}
}

func TestWaylandFamily(t *testing.T) {
	tests := []struct {
		env  func(string) string
		want string
	}{
		{envMap(), ""},
		{envMap("XDG_SESSION_TYPE", "x11"), ""},
		{envMap("XDG_SESSION_TYPE", "wayland", "XDG_CURRENT_DESKTOP", "GNOME"), "gnome"},
		{envMap("WAYLAND_DISPLAY", "wayland-0", "XDG_CURRENT_DESKTOP", "ubuntu:GNOME"), "gnome"},
		{envMap("XDG_SESSION_TYPE", "wayland", "XDG_CURRENT_DESKTOP", "KDE"), "kde"},
		{envMap("XDG_SESSION_TYPE", "wayland", "XDG_CURRENT_DESKTOP", "sway"), "wlroots"},
		{envMap("XDG_SESSION_TYPE", "wayland"), "wlroots"},
	}
	for _, test := range tests {
		if got := waylandFamily(test.env); got != test.want {
			t.Errorf("waylandFamily = %q, want %q", got, test.want)
		}
	}
}

func TestSessionEnv(t *testing.T) {
	env := envMap("WAYLAND_DISPLAY", "wayland-0", "XDG_SESSION_TYPE", "wayland")
	got := sessionEnv(env)
	want := []string{"WAYLAND_DISPLAY=wayland-0", "XDG_SESSION_TYPE=wayland"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("sessionEnv = %v, want %v", got, want)
	}
}

func TestOwnsCommandPath(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "sshdesk")
	if err := os.WriteFile(binary, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sshdesk-local")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	relative := filepath.Join(root, "sshdesk-remote")
	if err := os.Symlink("sshdesk", relative); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "sshdesk-bench")
	if err := os.WriteFile(foreign, []byte("not ours"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreignLink := filepath.Join(root, "sshdesk-agent")
	if err := os.Symlink("/bin/true", foreignLink); err != nil {
		t.Fatal(err)
	}
	owns := func(path string) bool {
		return ownsCommandPath(path, binary, os.Lstat, os.Readlink)
	}
	if !owns(binary) {
		t.Error("the binary itself must be owned")
	}
	if !owns(link) {
		t.Error("an absolute symlink into the binary must be owned")
	}
	if !owns(relative) {
		t.Error("a relative symlink into the binary must be owned")
	}
	if owns(foreign) {
		t.Error("a foreign plain file must not be owned")
	}
	if owns(foreignLink) {
		t.Error("a symlink to another target must not be owned")
	}
	if owns(filepath.Join(root, "missing")) {
		t.Error("a missing path must not be owned")
	}
}

func TestCopySelfBinary(t *testing.T) {
	root := t.TempDir()
	self := filepath.Join(root, "self")
	if err := os.WriteFile(self, []byte("payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "dest")
	if err := copySelfBinary(self, dest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || string(data) != "payload" {
		t.Fatalf("dest = %q, %v", data, err)
	}
	info, err := os.Stat(dest)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("dest mode = %v, %v", info.Mode(), err)
	}
	if err := copySelfBinary(dest, dest); err != nil {
		t.Fatalf("self copy must be a no-op: %v", err)
	}
}

func TestInstallSymlinksRefusesForeignFiles(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "sshdesk")
	if err := os.WriteFile(binary, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "sshdesk-local")
	if err := os.WriteFile(foreign, []byte("not ours"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installSymlinks(root, binary); err == nil {
		t.Fatal("installSymlinks must refuse to overwrite a foreign file")
	}
	data, _ := os.ReadFile(foreign)
	if string(data) != "not ours" {
		t.Fatal("the foreign file was modified")
	}
	// Stale symlinks are replaced.
	if err := os.Remove(foreign); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/bin/true", foreign); err != nil {
		t.Fatal(err)
	}
	if err := installSymlinks(root, binary); err != nil {
		t.Fatal(err)
	}
	target, err := os.Readlink(foreign)
	if err != nil || target != binary {
		t.Fatalf("symlink target = %q, %v", target, err)
	}
}

func TestRemoveOwnedCommandsKeepsForeignFiles(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "sshdesk")
	if err := os.WriteFile(binary, []byte("bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "sshdesk-local")
	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "sshdesk-bench")
	if err := os.WriteFile(foreign, []byte("not ours"), 0o755); err != nil {
		t.Fatal(err)
	}
	d, stdout, _ := fakeDeps()
	removeOwnedCommands(d, root, binary)
	if fileExists(binary) || fileExists(link) {
		t.Error("owned files must be removed")
	}
	if !fileExists(foreign) {
		t.Error("the foreign file must be kept")
	}
	if !strings.Contains(stdout.String(), "Keeping "+foreign) {
		t.Errorf("missing keep note in output:\n%s", stdout.String())
	}
}

func TestValidateAccount(t *testing.T) {
	d, _, _ := fakeDeps()
	d.LookupUser = func(name string) (string, int, int, error) {
		if name == "alice" {
			return "/home/alice", 1000, 1001, nil
		}
		return "", 0, 0, errors.New("unknown")
	}
	if _, _, _, err := validateAccount(d, "al ice"); err == nil {
		t.Error("invalid characters must be rejected")
	}
	if _, _, _, err := validateAccount(d, "bob"); err == nil {
		t.Error("unknown users must be rejected")
	}
	home, uid, gid, err := validateAccount(d, "alice")
	if err != nil || home != "/home/alice" || uid != 1000 || gid != 1001 {
		t.Errorf("validateAccount(alice) = %q, %d, %d, %v", home, uid, gid, err)
	}
}
