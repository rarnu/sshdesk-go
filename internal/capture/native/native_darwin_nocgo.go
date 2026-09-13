//go:build darwin && !cgo

package native

import "errors"

// New reports that the Quartz capture needs cgo, which a CGO_ENABLED=0
// build cannot provide.
func New() (*Capture, error) {
	return nil, errors.New("native capture on macOS requires a cgo-enabled build")
}
