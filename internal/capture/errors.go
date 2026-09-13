package capture

import "errors"

var (
	errTargetSize = errors.New("capture target dimensions must be between 1 and 16384")
	errFrameRate  = errors.New("capture FPS must be between 0.5 and 120")
)
