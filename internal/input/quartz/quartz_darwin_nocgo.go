//go:build darwin && !cgo

package quartz

import "errors"

// New reports that the Quartz input backend needs cgo, which a
// CGO_ENABLED=0 build cannot provide.
func New() (*Backend, error) {
	return nil, errors.New("quartz input on macOS requires a cgo-enabled build")
}
