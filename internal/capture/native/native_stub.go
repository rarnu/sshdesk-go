//go:build !darwin && !windows

package native

import "errors"

// New reports the same availability boundary as the Python constructor.
func New() (*Capture, error) {
	return nil, errors.New("native capture is available only on Windows and macOS")
}
