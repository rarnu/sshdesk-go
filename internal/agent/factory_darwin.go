//go:build darwin

package agent

import (
	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/capture/native"
	"github.com/rarnu/sshdesk-go/internal/input"
	"github.com/rarnu/sshdesk-go/internal/input/quartz"
)

// defaultFactories resolves the macOS platform backends: Quartz capture and
// Quartz input, matching the Python detect_platform (aqua/native/quartz).
func defaultFactories() (func() (capture.ScreenCapture, error), func(capture.ScreenCapture) (input.Backend, error)) {
	return func() (capture.ScreenCapture, error) {
			return native.New()
		},
		func(capture.ScreenCapture) (input.Backend, error) {
			return quartz.New()
		}
}
