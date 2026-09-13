//go:build !linux && !darwin && !windows

package main

import (
	"errors"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/synthetic"
	"github.com/rylena/sshdesk-go/internal/input"
)

// autoCapture keeps the synthetic development capture until the macOS and
// Windows native backends land.
func autoCapture(animate bool, display string) (capture.ScreenCapture, error) {
	return synthetic.NewCapture(1280, 720, animate), nil
}

func newX11Capture(display string) (capture.ScreenCapture, error) {
	return nil, errors.New(`capture backend "x11" is only available on Linux`)
}

func newX11Input(display string) (input.Backend, error) {
	return nil, errors.New(`input backend "x11" is only available on Linux`)
}

// resolveAutoInput keeps input disabled for the synthetic development
// sessions until the macOS and Windows input backends land.
func resolveAutoInput(captureName string) (backend string, null bool, err error) {
	return "", true, nil
}
