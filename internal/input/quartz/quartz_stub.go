//go:build !darwin

package quartz

import "errors"

// New reports the platform boundary; Quartz input is macOS-only.
func New() (*Backend, error) {
	return nil, errors.New(`input backend "quartz" is only available on macOS`)
}
