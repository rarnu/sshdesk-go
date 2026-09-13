// Package config reads the root-owned per-account whitelist configuration
// file /etc/sshdesk/<account>.conf. Only a fixed set of keys is honored and
// nothing from the file is ever evaluated.
package config

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

// Keys is the fixed whitelist accepted from the per-account file.
var Keys = []string{
	"DISPLAY",
	"XAUTHORITY",
	"WAYLAND_DISPLAY",
	"XDG_RUNTIME_DIR",
	"XDG_SESSION_TYPE",
	"XDG_CURRENT_DESKTOP",
	"DBUS_SESSION_BUS_ADDRESS",
	"YDOTOOL_SOCKET",
	"RUN_AS",
	"SSHDESK_RENDER",
	"SSHDESK_COLOR",
	"SSHDESK_MOUSE",
	"SSHDESK_UNICODE",
	"SSHDESK_X11_CAPTURE",
	"SSHDESK_MAX_FPS",
	"SSHDESK_SCALE",
}

var sshdeskKeys = []string{
	"SSHDESK_RENDER",
	"SSHDESK_COLOR",
	"SSHDESK_MOUSE",
	"SSHDESK_UNICODE",
	"SSHDESK_X11_CAPTURE",
	"SSHDESK_MAX_FPS",
	"SSHDESK_SCALE",
}

var runAsPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func whitelist() map[string]bool {
	allowed := make(map[string]bool, len(Keys))
	for _, key := range Keys {
		allowed[key] = true
	}
	return allowed
}

func accountName() (string, error) {
	current, err := user.Current()
	if err == nil && current.Username != "" {
		return current.Username, nil
	}
	if name := os.Getenv("USER"); name != "" {
		return name, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("could not determine the current account")
}

// Path returns the configuration file location for the given account.
func Path(account string) string {
	return filepath.Join("/etc/sshdesk", account+".conf")
}

// Values returns the effective SSHDESK environment values for the current
// account. Whitelisted keys from /etc/sshdesk/<account>.conf override the
// process environment, then defaults are applied for DISPLAY, XAUTHORITY,
// RUN_AS, and the SSHDESK_* keys.
func Values() (map[string]string, error) {
	account, err := accountName()
	if err != nil {
		return nil, fmt.Errorf("could not determine the current account: %w", err)
	}
	return values(account, os.ReadFile, os.Getenv)
}

func values(account string, readFile func(string) ([]byte, error), getenv func(string) string) (map[string]string, error) {
	allowed := whitelist()
	resolved := make(map[string]string, len(Keys))
	for _, key := range Keys {
		resolved[key] = getenv(key)
	}
	data, err := readFile(Path(account))
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			key, value, found := strings.Cut(line, "=")
			if !found || !allowed[key] {
				continue
			}
			resolved[key] = strings.TrimRight(value, "\r")
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if resolved["DISPLAY"] == "" {
		resolved["DISPLAY"] = ":0"
	}
	if resolved["XAUTHORITY"] == "" {
		resolved["XAUTHORITY"] = filepath.Join(getenv("HOME"), ".Xauthority")
	}
	if resolved["RUN_AS"] == "" {
		resolved["RUN_AS"] = account
	}
	if !runAsPattern.MatchString(resolved["RUN_AS"]) {
		return nil, fmt.Errorf("invalid SSHDESK desktop account")
	}
	for _, key := range sshdeskKeys {
		if resolved[key] == "" {
			resolved[key] = "auto"
		}
	}
	return resolved, nil
}

// Apply resolves the configuration for the current account and installs the
// effective values into the process environment.
func Apply() (map[string]string, error) {
	values, err := Values()
	if err != nil {
		return nil, err
	}
	for _, key := range Keys {
		if values[key] == "" {
			os.Unsetenv(key)
			continue
		}
		os.Setenv(key, values[key])
	}
	return values, nil
}
