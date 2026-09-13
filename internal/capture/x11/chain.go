// Package x11 is the composite X11 root-window capture: an FFmpeg/XCB
// stream tier, an MIT-SHM tier, and an xgb GetImage fallback, with
// per-second geometry polling. The fallback-chain decisions live here so
// they can be tested without an X server; the X wiring is Linux-only.
package x11

import "image"

// Frame is one captured tier image at target size.
type Frame struct {
	Image         *image.RGBA
	CapturedNs    int64
	ContentDigest []byte
}

// tier is one capture level of the fallback chain.
type tier interface {
	capture() (Frame, error)
	close()
}

// chain walks ffmpeg → xshm → fallback, permanently disabling a tier after
// its first failure (unless that tier was explicitly requested).
type chain struct {
	backend        string
	ffmpegDisabled bool
	sharedDisabled bool
	ffmpeg         tier
	shared         tier
	newFFmpeg      func() (tier, error)
	newShared      func() (tier, error)
	fallback       func() (Frame, error)
}

func (c *chain) closeFFmpeg() {
	if c.ffmpeg != nil {
		c.ffmpeg.close()
		c.ffmpeg = nil
	}
}

func (c *chain) closeShared() {
	if c.shared != nil {
		c.shared.close()
		c.shared = nil
	}
}

func (c *chain) ffmpegCapture() (Frame, error) {
	if c.ffmpeg == nil {
		t, err := c.newFFmpeg()
		if err != nil {
			return Frame{}, err
		}
		c.ffmpeg = t
	}
	return c.ffmpeg.capture()
}

func (c *chain) sharedCapture() (Frame, error) {
	if c.shared == nil {
		t, err := c.newShared()
		if err != nil {
			return Frame{}, err
		}
		c.shared = t
	}
	return c.shared.capture()
}

func (c *chain) capture() (Frame, error) {
	if !c.ffmpegDisabled {
		frame, err := c.ffmpegCapture()
		if err == nil {
			return frame, nil
		}
		c.closeFFmpeg()
		if c.backend == "ffmpeg" {
			return Frame{}, err
		}
		c.ffmpegDisabled = true
	}
	if !c.sharedDisabled {
		frame, err := c.sharedCapture()
		if err == nil {
			return frame, nil
		}
		c.closeShared()
		if c.backend == "xshm" {
			return Frame{}, err
		}
		c.sharedDisabled = true
	}
	return c.fallback()
}

func (c *chain) close() {
	c.closeFFmpeg()
	c.closeShared()
}
