//go:build windows

package agent

import (
	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/native"
	"github.com/rylena/sshdesk-go/internal/input"
	"github.com/rylena/sshdesk-go/internal/input/sendinput"
)

// defaultFactories resolves the Windows platform backends: BitBlt capture
// and SendInput, matching the Python detect_platform
// (windows/native/sendinput).
func defaultFactories() (func() (capture.ScreenCapture, error), func(capture.ScreenCapture) (input.Backend, error)) {
	return func() (capture.ScreenCapture, error) {
			return native.New()
		},
		func(capture.ScreenCapture) (input.Backend, error) {
			return sendinput.New()
		}
}
