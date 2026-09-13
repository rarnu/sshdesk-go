package main

import (
	"fmt"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/gnome"
	"github.com/rylena/sshdesk-go/internal/capture/native"
	"github.com/rylena/sshdesk-go/internal/capture/synthetic"
	"github.com/rylena/sshdesk-go/internal/capture/wayland"
	"github.com/rylena/sshdesk-go/internal/input"
	"github.com/rylena/sshdesk-go/internal/input/quartz"
	"github.com/rylena/sshdesk-go/internal/input/sendinput"
	"github.com/rylena/sshdesk-go/internal/input/ydotool"
)

// createCapture builds the capture backend selected by --capture, mirroring
// the Python platform.create_capture.
func createCapture(name string, animate bool, display string) (capture.ScreenCapture, error) {
	switch name {
	case "synthetic":
		return synthetic.NewCapture(1280, 720, animate), nil
	case "auto":
		return autoCapture(animate, display)
	case "x11":
		return newX11Capture(display)
	case "wayland":
		return wayland.New()
	case "gnome":
		return gnome.New()
	case "native":
		return native.New()
	default:
		return nil, fmt.Errorf("unknown capture backend: %s", name)
	}
}

// createInput builds the input backend selected by --input, mirroring the
// Python platform.create_input. Synthetic development sessions keep input
// disabled even under --input auto.
func createInput(name string, disabled bool, captureName, display string, captureBackend capture.ScreenCapture) (input.Backend, error) {
	if disabled || name == "none" {
		return input.NullBackend{}, nil
	}
	backend := name
	if backend == "auto" {
		resolved, null, err := resolveAutoInput(captureName)
		if err != nil {
			return nil, err
		}
		if null {
			return input.NullBackend{}, nil
		}
		backend = resolved
	}
	switch backend {
	case "x11":
		return newX11Input(display)
	case "ydotool":
		return ydotool.New()
	case "mutter":
		provider, ok := captureBackend.(interface {
			CreateInputBackend() (input.Backend, error)
		})
		if ok {
			return provider.CreateInputBackend()
		}
		return nil, fmt.Errorf("Mutter input requires a linked GNOME capture session")
	case "quartz":
		return quartz.New()
	case "sendinput":
		return sendinput.New()
	default:
		return nil, fmt.Errorf("unknown input backend: %s", backend)
	}
}
