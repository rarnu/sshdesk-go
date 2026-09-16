package setup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseEnviron(t *testing.T) {
	tests := []struct {
		name string
		data string
		want map[string]string
	}{
		{"empty", "", map[string]string{}},
		{"trailing NUL", "A=1\x00B=2\x00", map[string]string{"A": "1", "B": "2"}},
		{"no trailing NUL", "A=1\x00B=2", map[string]string{"A": "1", "B": "2"}},
		{"fields without key or value separator are skipped",
			"A=1\x00BADFIELD\x00=novalue\x00C=x=y\x00",
			map[string]string{"A": "1", "C": "x=y"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseEnviron([]byte(test.data))
			if len(got) != len(test.want) {
				t.Fatalf("parseEnviron = %v, want %v", got, test.want)
			}
			for key, value := range test.want {
				if got[key] != value {
					t.Errorf("parseEnviron[%s] = %q, want %q", key, got[key], value)
				}
			}
		})
	}
}

// fakeProc describes one /proc/<pid> directory in a fake tree.
type fakeProc struct {
	uid     int
	comm    string
	environ string
}

// writeFakeProc builds a /proc-like tree and an ownerOf seam over it.
func writeFakeProc(t *testing.T, procs map[string]fakeProc) (string, func(os.FileInfo) (int, bool)) {
	t.Helper()
	root := t.TempDir()
	owners := map[string]int{}
	for pid, proc := range procs {
		dir := filepath.Join(root, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		owners[pid] = proc.uid
		if proc.comm != "" {
			if err := os.WriteFile(filepath.Join(dir, "comm"), []byte(proc.comm+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if proc.environ != "" {
			if err := os.WriteFile(filepath.Join(dir, "environ"), []byte(proc.environ), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	ownerOf := func(info os.FileInfo) (int, bool) {
		uid, ok := owners[info.Name()]
		return uid, ok
	}
	return root, ownerOf
}

func environ(pairs ...string) string {
	data := ""
	for i := 0; i+1 < len(pairs); i += 2 {
		data += pairs[i] + "=" + pairs[i+1] + "\x00"
	}
	return data
}

func TestScanSessionProcPrefersCompositor(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		// The generic candidate sorts first by pid but must lose.
		"100": {uid: 1000, comm: "firefox", environ: environ(
			"WAYLAND_DISPLAY", "wayland-1", "XDG_RUNTIME_DIR", "/run/user/1000")},
		"200": {uid: 1000, comm: "gnome-shell", environ: environ(
			"WAYLAND_DISPLAY", "wayland-0",
			"XDG_RUNTIME_DIR", "/run/user/1000",
			"XDG_SESSION_TYPE", "wayland",
			"XDG_CURRENT_DESKTOP", "GNOME",
			"DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus",
			"DISPLAY", ":0",
			"XAUTHORITY", "/run/user/1000/.mutter-Xwaylandauth.ABC123",
			"HOME", "/home/alice")},
	})
	got := scanSessionProc(root, 1000, ownerOf)
	if got["WAYLAND_DISPLAY"] != "wayland-0" {
		t.Fatalf("compositor environment must win: %v", got)
	}
	if got["XDG_SESSION_TYPE"] != "wayland" || got["XDG_CURRENT_DESKTOP"] != "GNOME" ||
		got["DBUS_SESSION_BUS_ADDRESS"] != "unix:path=/run/user/1000/bus" ||
		got["DISPLAY"] != ":0" ||
		got["XAUTHORITY"] != "/run/user/1000/.mutter-Xwaylandauth.ABC123" {
		t.Errorf("incomplete harvest: %v", got)
	}
	if _, ok := got["HOME"]; ok {
		t.Errorf("non-harvest keys must be dropped: %v", got)
	}
}

func TestScanSessionProcCompositorNameVariants(t *testing.T) {
	for _, comm := range []string{"plasmashell", "sway", "hyprland", "Hyprland",
		"weston", "wayfire", "labwc", "river", "kwin_wayland"} {
		root, ownerOf := writeFakeProc(t, map[string]fakeProc{
			"100": {uid: 1000, comm: comm, environ: environ(
				"WAYLAND_DISPLAY", "wayland-0", "XDG_RUNTIME_DIR", "/run/user/1000")},
		})
		if got := scanSessionProc(root, 1000, ownerOf); got["WAYLAND_DISPLAY"] != "wayland-0" {
			t.Errorf("compositor %q was not recognized", comm)
		}
	}
}

func TestScanSessionProcFallsBackToGenericCandidate(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		"100": {uid: 1000, comm: "some-session-client", environ: environ(
			"WAYLAND_DISPLAY", "wayland-2", "XDG_RUNTIME_DIR", "/run/user/1000")},
	})
	if got := scanSessionProc(root, 1000, ownerOf); got["WAYLAND_DISPLAY"] != "wayland-2" {
		t.Fatalf("generic candidate must be used: %v", got)
	}
}

func TestScanSessionProcFiltersByOwner(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		"100": {uid: 1001, comm: "gnome-shell", environ: environ(
			"WAYLAND_DISPLAY", "wayland-0", "XDG_RUNTIME_DIR", "/run/user/1001")},
	})
	if got := scanSessionProc(root, 1000, ownerOf); got != nil {
		t.Fatalf("another user's session must not be harvested: %v", got)
	}
}

func TestScanSessionProcAcceptsX11Session(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		// X11-only desktop (Cinnamon via lightdm): no WAYLAND_DISPLAY anywhere.
		"100": {uid: 1000, comm: "cinnamon", environ: environ(
			"DISPLAY", ":0",
			"XDG_RUNTIME_DIR", "/run/user/1000",
			"XDG_SESSION_TYPE", "x11",
			"XDG_CURRENT_DESKTOP", "X-Cinnamon",
			"XAUTHORITY", "/home/rarnu/.Xauthority")},
	})
	got := scanSessionProc(root, 1000, ownerOf)
	if got["DISPLAY"] != ":0" || got["XDG_SESSION_TYPE"] != "x11" ||
		got["XDG_CURRENT_DESKTOP"] != "X-Cinnamon" ||
		got["XAUTHORITY"] != "/home/rarnu/.Xauthority" {
		t.Fatalf("X11 session must be harvested: %v", got)
	}
}

func TestScanSessionProcSkipsForwardedDisplay(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		// ssh X11 forwarding: localhost displays do not mark a local session.
		"100": {uid: 1000, comm: "bash", environ: environ(
			"DISPLAY", "localhost:10.0", "XDG_RUNTIME_DIR", "/run/user/1000")},
		// A forwarded DISPLAY must not leak into the harvest either.
		"200": {uid: 1000, comm: "sway", environ: environ(
			"WAYLAND_DISPLAY", "wayland-0",
			"XDG_RUNTIME_DIR", "/run/user/1000",
			"DISPLAY", "localhost:11.0")},
	})
	got := scanSessionProc(root, 1000, ownerOf)
	if got["WAYLAND_DISPLAY"] != "wayland-0" {
		t.Fatalf("wayland compositor must win: %v", got)
	}
	if _, ok := got["DISPLAY"]; ok {
		t.Errorf("forwarded DISPLAY must be dropped from the harvest: %v", got)
	}
}

func TestScanSessionProcRequiresDisplayAndRuntimeDir(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		// Wayland display without a runtime directory: incomplete.
		"100": {uid: 1000, comm: "sway", environ: environ(
			"WAYLAND_DISPLAY", "wayland-0")},
		// Runtime directory without any display: not graphical.
		"200": {uid: 1000, comm: "systemd", environ: environ(
			"XDG_RUNTIME_DIR", "/run/user/1000")},
	})
	if got := scanSessionProc(root, 1000, ownerOf); got != nil {
		t.Fatalf("incomplete candidates must be skipped: %v", got)
	}
}

func TestScanSessionProcSkipsNonNumericAndBrokenEntries(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		"self": {uid: 1000, comm: "gnome-shell", environ: environ(
			"WAYLAND_DISPLAY", "wayland-0", "XDG_RUNTIME_DIR", "/run/user/1000")},
		"bus": {uid: 1000, comm: "sway", environ: environ(
			"WAYLAND_DISPLAY", "wayland-1", "XDG_RUNTIME_DIR", "/run/user/1000")},
		// Numeric but without an environ file (kernel thread style).
		"300": {uid: 1000, comm: "kworker"},
	})
	if got := scanSessionProc(root, 1000, ownerOf); got != nil {
		t.Fatalf("non-numeric and broken entries must be skipped: %v", got)
	}
}

func TestScanSessionProcDropsEmptyValues(t *testing.T) {
	root, ownerOf := writeFakeProc(t, map[string]fakeProc{
		"100": {uid: 1000, comm: "sway", environ: environ(
			"WAYLAND_DISPLAY", "wayland-0",
			"XDG_RUNTIME_DIR", "/run/user/1000",
			"XDG_CURRENT_DESKTOP", "")},
	})
	got := scanSessionProc(root, 1000, ownerOf)
	if _, ok := got["XDG_CURRENT_DESKTOP"]; ok {
		t.Errorf("empty values must be dropped: %v", got)
	}
}

func TestScanSessionProcMissingRoot(t *testing.T) {
	if got := scanSessionProc(filepath.Join(t.TempDir(), "nope"), 1000,
		func(os.FileInfo) (int, bool) { return 1000, true }); got != nil {
		t.Fatalf("a missing /proc root must yield nil: %v", got)
	}
}
