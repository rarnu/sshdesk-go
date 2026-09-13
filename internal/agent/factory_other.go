//go:build !linux && !darwin && !windows

package agent

import (
	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/synthetic"
	"github.com/rylena/sshdesk-go/internal/input"
)

// defaultFactories keeps the development backends until the macOS and
// Windows platform backends land.
func defaultFactories() (func() (capture.ScreenCapture, error), func(capture.ScreenCapture) (input.Backend, error)) {
	return func() (capture.ScreenCapture, error) {
			return synthetic.NewCapture(1280, 720, true), nil
		},
		func(capture.ScreenCapture) (input.Backend, error) {
			return input.NullBackend{}, nil
		}
}
