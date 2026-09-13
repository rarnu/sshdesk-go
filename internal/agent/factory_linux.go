//go:build linux

package agent

import (
	"fmt"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/gnome"
	"github.com/rylena/sshdesk-go/internal/capture/wayland"
	x11capture "github.com/rylena/sshdesk-go/internal/capture/x11"
	"github.com/rylena/sshdesk-go/internal/input"
	x11input "github.com/rylena/sshdesk-go/internal/input/x11"
	"github.com/rylena/sshdesk-go/internal/input/ydotool"
)

// defaultFactories resolves capture/input through platform detection on
// Linux.
func defaultFactories() (func() (capture.ScreenCapture, error), func(capture.ScreenCapture) (input.Backend, error)) {
	return func() (capture.ScreenCapture, error) {
			selected, err := DetectPlatform()
			if err != nil {
				return nil, err
			}
			switch selected.Capture {
			case "x11":
				return x11capture.NewCapture("")
			case "wayland":
				return wayland.New()
			case "gnome":
				return gnome.New()
			default:
				return nil, fmt.Errorf("capture backend %q is not implemented yet", selected.Capture)
			}
		},
		func(captureBackend capture.ScreenCapture) (input.Backend, error) {
			selected, err := DetectPlatform()
			if err != nil {
				return nil, err
			}
			switch selected.Input {
			case "x11":
				return x11input.NewInput("")
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
			default:
				return nil, fmt.Errorf("input backend %q is not implemented yet", selected.Input)
			}
		}
}
