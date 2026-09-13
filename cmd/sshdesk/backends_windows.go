//go:build windows

package main

import (
	"errors"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/native"
	"github.com/rylena/sshdesk-go/internal/input"
)

// autoCapture selects the BitBlt native capture, matching the Python
// detect_platform (Windows → windows/native/sendinput).
func autoCapture(animate bool, display string) (capture.ScreenCapture, error) {
	return native.New()
}

func newX11Capture(display string) (capture.ScreenCapture, error) {
	return nil, errors.New(`capture backend "x11" is only available on Linux`)
}

func newX11Input(display string) (input.Backend, error) {
	return nil, errors.New(`input backend "x11" is only available on Linux`)
}

// resolveAutoInput selects SendInput, matching the Python detect_platform.
func resolveAutoInput(captureName string) (backend string, null bool, err error) {
	return "sendinput", false, nil
}
