package setup

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// compositorNames are the process names (from /proc/<pid>/comm) considered
// compositors or session leaders. When several processes owned by the target
// account carry a graphical environment, a compositor's environment wins.
var compositorNames = map[string]bool{
	"gnome-shell":  true,
	"plasmashell":  true,
	"sway":         true,
	"hyprland":     true,
	"Hyprland":     true,
	"weston":       true,
	"wayfire":      true,
	"labwc":        true,
	"river":        true,
	"kwin_wayland": true,
}

// harvestKeys are the variables extracted from the session process
// environment, in config file order.
var harvestKeys = []string{
	"WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XDG_SESSION_TYPE",
	"XDG_CURRENT_DESKTOP", "DBUS_SESSION_BUS_ADDRESS", "DISPLAY",
	"XAUTHORITY",
}

// parseEnviron parses the NUL-separated KEY=VALUE format of
// /proc/<pid>/environ. Fields without '=' or with an empty key are skipped.
func parseEnviron(data []byte) map[string]string {
	env := map[string]string{}
	for _, field := range strings.Split(string(data), "\x00") {
		key, value, ok := strings.Cut(field, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	return env
}

// extractSessionEnv keeps the harvest keys with non-empty values.
func extractSessionEnv(env map[string]string) map[string]string {
	harvested := map[string]string{}
	for _, key := range harvestKeys {
		if value := env[key]; value != "" {
			harvested[key] = value
		}
	}
	if len(harvested) == 0 {
		return nil
	}
	return harvested
}

// scanSessionProc walks a /proc-like tree and collects the graphical session
// environment of processes owned by uid. A process is a candidate when its
// environ holds both WAYLAND_DISPLAY and XDG_RUNTIME_DIR; compositor
// processes are preferred over generic candidates. ownerOf reports the
// numeric owner of a process directory and is the platform seam (the real
// one lives in sessionenv_linux.go); root points at the tree to scan so
// tests can pass a fake /proc.
func scanSessionProc(root string, uid int, ownerOf func(os.FileInfo) (int, bool)) map[string]string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var fallback map[string]string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		owner, ok := ownerOf(info)
		if !ok || owner != uid {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, entry.Name(), "environ"))
		if err != nil {
			continue
		}
		env := parseEnviron(data)
		if env["WAYLAND_DISPLAY"] == "" || env["XDG_RUNTIME_DIR"] == "" {
			continue
		}
		comm, _ := os.ReadFile(filepath.Join(root, entry.Name(), "comm"))
		if compositorNames[strings.TrimSpace(string(comm))] {
			return extractSessionEnv(env)
		}
		if fallback == nil {
			fallback = extractSessionEnv(env)
		}
	}
	return fallback
}
