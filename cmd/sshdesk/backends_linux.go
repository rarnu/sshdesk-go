//go:build linux

package main

import (
	"errors"
	"os"
	"strings"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/capture/gnome"
	"github.com/rarnu/sshdesk-go/internal/capture/wayland"
	x11capture "github.com/rarnu/sshdesk-go/internal/capture/x11"
	"github.com/rarnu/sshdesk-go/internal/input"
	x11input "github.com/rarnu/sshdesk-go/internal/input/x11"
)

// autoCapture resolves --capture auto like the Python detect_platform.
func autoCapture(animate bool, display string) (capture.ScreenCapture, error) {
	if os.Getenv("WAYLAND_DISPLAY") != "" && strings.ToLower(os.Getenv("XDG_SESSION_TYPE")) != "x11" {
		desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
		if strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity") {
			return gnome.New()
		}
		return wayland.New()
	}
	if os.Getenv("DISPLAY") == "" && display == "" {
		return nil, errors.New("no Linux graphical session found (DISPLAY or WAYLAND_DISPLAY)")
	}
	return newX11Capture(display)
}

func newX11Capture(display string) (capture.ScreenCapture, error) {
	return x11capture.NewCapture(display)
}

func newX11Input(display string) (input.Backend, error) {
	return x11input.NewInput(display)
}

// resolveAutoInput picks the --input auto backend like the Python
// detect_platform; null reports that input stays disabled for synthetic
// development sessions.
func resolveAutoInput(captureName string) (backend string, null bool, err error) {
	if captureName == "synthetic" {
		return "", true, nil
	}
	if os.Getenv("WAYLAND_DISPLAY") != "" && strings.ToLower(os.Getenv("XDG_SESSION_TYPE")) != "x11" {
		desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP"))
		if strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity") {
			return "mutter", false, nil
		}
		return "ydotool", false, nil
	}
	if os.Getenv("DISPLAY") != "" {
		return "x11", false, nil
	}
	return "", false, errors.New("no Linux graphical session found (DISPLAY or WAYLAND_DISPLAY)")
}
