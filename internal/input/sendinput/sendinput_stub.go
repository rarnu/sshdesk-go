//go:build !windows

package sendinput

import "errors"

// New reports the platform boundary; SendInput is Windows-only.
func New() (*Backend, error) {
	return nil, errors.New(`input backend "sendinput" is only available on Windows`)
}
