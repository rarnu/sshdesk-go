package agent

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/input"
)

// buttons maps pointer button names to their desktop button numbers.
var buttons = map[string]int{"left": 1, "middle": 2, "right": 3}

// keyNames maps accepted key names to their normalized key codes.
var keyNames = map[string]input.KeyCode{
	"enter":     input.KeyEnter,
	"escape":    input.KeyEscape,
	"backspace": input.KeyBackspace,
	"tab":       input.KeyTab,
	"up":        input.KeyUp,
	"down":      input.KeyDown,
	"right":     input.KeyRight,
	"left":      input.KeyLeft,
	"home":      input.KeyHome,
	"end":       input.KeyEnd,
	"page-up":   input.KeyPageUp,
	"page-down": input.KeyPageDown,
	"insert":    input.KeyInsert,
	"delete":    input.KeyDelete,
	"f1":        input.KeyF1,
	"f2":        input.KeyF2,
	"f3":        input.KeyF3,
	"f4":        input.KeyF4,
	"f5":        input.KeyF5,
	"f6":        input.KeyF6,
	"f7":        input.KeyF7,
	"f8":        input.KeyF8,
	"f9":        input.KeyF9,
	"f10":       input.KeyF10,
	"f11":       input.KeyF11,
	"f12":       input.KeyF12,
}

// KeyNames lists the accepted key names in definition order (for validation).
func KeyNames() map[string]input.KeyCode { return keyNames }

// PlatformInfo mirrors the Python PlatformSelection namedtuple.
type PlatformInfo struct {
	System  string
	Session string
	Capture string
	Input   string
}

// platformGetenv is a seam so tests can drive platform detection.
var platformGetenv = os.Getenv

// DetectPlatform resolves the platform/session/backend triple like the
// Python detect_platform. Real backends land in later phases; the controller
// keeps using its injected capture/input regardless.
func DetectPlatform() (PlatformInfo, error) {
	switch runtime.GOOS {
	case "linux":
		session := strings.ToLower(platformGetenv("XDG_SESSION_TYPE"))
		if platformGetenv("WAYLAND_DISPLAY") != "" && session != "x11" {
			desktop := strings.ToLower(platformGetenv("XDG_CURRENT_DESKTOP"))
			if strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity") {
				return PlatformInfo{"Linux", "wayland", "gnome", "mutter"}, nil
			}
			return PlatformInfo{"Linux", "wayland", "wayland", "ydotool"}, nil
		}
		if platformGetenv("DISPLAY") != "" {
			return PlatformInfo{"Linux", "x11", "x11", "x11"}, nil
		}
		return PlatformInfo{}, fmt.Errorf("no Linux graphical session found (DISPLAY or WAYLAND_DISPLAY)")
	case "darwin":
		return PlatformInfo{"Darwin", "aqua", "native", "quartz"}, nil
	case "windows":
		return PlatformInfo{"Windows", "windows", "native", "sendinput"}, nil
	}
	return PlatformInfo{}, fmt.Errorf("unsupported server operating system: %s", runtime.GOOS)
}

// Controller serves low-frequency computer-use commands for an authenticated
// SSH agent. Capture and input backends are created lazily.
type Controller struct {
	captureBackend capture.ScreenCapture
	inputBackend   input.Backend

	platform   func() (PlatformInfo, error)
	newCapture func() (capture.ScreenCapture, error)
	newInput   func(capture.ScreenCapture) (input.Backend, error)
}

// NewController builds a controller on the platform default backends
// (X11 on Linux graphical sessions, synthetic/null elsewhere until the
// remaining platform backends land).
func NewController() *Controller {
	captureFactory, inputFactory := defaultFactories()
	return &Controller{
		platform:   DetectPlatform,
		newCapture: captureFactory,
		newInput:   inputFactory,
	}
}

// SetBackends injects premade backends (tests and later-phase wiring).
func (c *Controller) SetBackends(captureBackend capture.ScreenCapture, inputBackend input.Backend) {
	c.captureBackend = captureBackend
	c.inputBackend = inputBackend
}

// SetPlatform overrides platform detection (tests).
func (c *Controller) SetPlatform(detect func() (PlatformInfo, error)) {
	c.platform = detect
}

// Capture returns the lazily created capture backend.
func (c *Controller) Capture() (capture.ScreenCapture, error) {
	if c.captureBackend == nil {
		backend, err := c.newCapture()
		if err != nil {
			return nil, err
		}
		c.captureBackend = backend
	}
	return c.captureBackend, nil
}

// Input returns the lazily created input backend.
func (c *Controller) Input() (input.Backend, error) {
	if c.inputBackend == nil {
		captureBackend, err := c.Capture()
		if err != nil {
			return nil, err
		}
		backend, err := c.newInput(captureBackend)
		if err != nil {
			return nil, err
		}
		c.inputBackend = backend
	}
	return c.inputBackend, nil
}

// Info reports platform and desktop geometry.
func (c *Controller) Info() (map[string]any, error) {
	selected, err := c.platform()
	if err != nil {
		return nil, err
	}
	captureBackend, err := c.Capture()
	if err != nil {
		return nil, err
	}
	width, height := captureBackend.Size()
	return map[string]any{
		"platform": selected.System,
		"session":  selected.Session,
		"capture":  selected.Capture,
		"input":    selected.Input,
		"width":    width,
		"height":   height,
	}, nil
}

// Screenshot captures a PNG, downscaling to maxWidth with a bilinear filter
// when the desktop is wider. It returns the PNG bytes and the desktop size.
func (c *Controller) Screenshot(maxWidth int) ([]byte, int, int, error) {
	captureBackend, err := c.Capture()
	if err != nil {
		return nil, 0, 0, err
	}
	frame, err := captureBackend.Capture()
	if err != nil {
		return nil, 0, 0, err
	}
	img := frame.RGBAImage()
	if maxWidth > 0 && img.Rect.Dx() > maxWidth {
		height := max(1, int(math.RoundToEven(float64(img.Rect.Dy())*float64(maxWidth)/float64(img.Rect.Dx()))))
		img = resizeBilinear(img, maxWidth, height)
	}
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, img); err != nil {
		return nil, 0, 0, err
	}
	return output.Bytes(), frame.Width(), frame.Height(), nil
}

// resizeBilinear scales img to (width, height) with a bilinear filter.
func resizeBilinear(src *image.RGBA, width, height int) *image.RGBA {
	srcW := src.Rect.Dx()
	srcH := src.Rect.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	if width == 0 || height == 0 {
		return dst
	}
	xRatio := float64(srcW) / float64(width)
	yRatio := float64(srcH) / float64(height)
	for y := 0; y < height; y++ {
		srcY := (float64(y)+0.5)*yRatio - 0.5
		y0 := int(math.Floor(srcY))
		fy := srcY - float64(y0)
		y0 = min(max(y0, 0), srcH-1)
		y1 := min(y0+1, srcH-1)
		for x := 0; x < width; x++ {
			srcX := (float64(x)+0.5)*xRatio - 0.5
			x0 := int(math.Floor(srcX))
			fx := srcX - float64(x0)
			x0 = min(max(x0, 0), srcW-1)
			x1 := min(x0+1, srcW-1)
			i00 := src.PixOffset(x0, y0)
			i10 := src.PixOffset(x1, y0)
			i01 := src.PixOffset(x0, y1)
			i11 := src.PixOffset(x1, y1)
			di := dst.PixOffset(x, y)
			for channel := 0; channel < 4; channel++ {
				top := float64(src.Pix[i00+channel])*(1-fx) + float64(src.Pix[i10+channel])*fx
				bottom := float64(src.Pix[i01+channel])*(1-fx) + float64(src.Pix[i11+channel])*fx
				dst.Pix[di+channel] = uint8(top*(1-fy) + bottom*fy + 0.5)
			}
		}
	}
	return dst
}

// Move positions the pointer.
func (c *Controller) Move(x, y int) error {
	backend, err := c.Input()
	if err != nil {
		return err
	}
	backend.Move(x, y)
	return nil
}

// Click presses and releases a button count times, 0.05s between repetitions.
func (c *Controller) Click(x, y int, button string, count int) error {
	number, ok := buttons[button]
	if !ok {
		return fmt.Errorf("button must be left, middle, or right")
	}
	backend, err := c.Input()
	if err != nil {
		return err
	}
	bounded := max(1, min(count, 20))
	for index := 0; index < bounded; index++ {
		backend.Button(number, true, x, y)
		backend.Button(number, false, x, y)
		if index+1 < bounded {
			time.Sleep(50 * time.Millisecond)
		}
	}
	return nil
}

// Scroll injects a scroll event; the amount is clamped to ±20.
func (c *Controller) Scroll(amount, x, y int) error {
	backend, err := c.Input()
	if err != nil {
		return err
	}
	backend.Scroll(max(-20, min(20, amount)), x, y)
	return nil
}

// TypeText taps one CHARACTER key per Unicode code point.
func (c *Controller) TypeText(text string, intervalMs float64) error {
	if utf8.RuneCountInString(text) > maxTextLength {
		return fmt.Errorf("text is limited to %d characters", maxTextLength)
	}
	backend, err := c.Input()
	if err != nil {
		return err
	}
	delay := max(0.0, min(intervalMs, 1000.0)) / 1000
	for _, character := range text {
		backend.Key(input.KeyEvent{Action: input.KeyTap, Code: input.KeyCharacter, Unicode: character})
		if delay > 0 {
			time.Sleep(time.Duration(delay * float64(time.Second)))
		}
	}
	return nil
}

// Key taps one named key with the given modifier bitmask.
func (c *Controller) Key(name string, modifiers int) error {
	code, ok := keyNames[strings.ToLower(name)]
	if !ok {
		return fmt.Errorf("unknown key: %s", name)
	}
	backend, err := c.Input()
	if err != nil {
		return err
	}
	backend.Key(input.KeyEvent{Action: input.KeyTap, Modifiers: input.Modifiers(modifiers), Code: code})
	return nil
}

// Close releases any lazily created backends.
func (c *Controller) Close() {
	if c.inputBackend != nil {
		c.inputBackend.Close()
		c.inputBackend = nil
	}
	if c.captureBackend != nil {
		c.captureBackend.Close()
		c.captureBackend = nil
	}
}

// modifiers folds ctrl/alt/shift truthy flags into the modifier bitmask
// (SHIFT=1, ALT=2, CTRL=4), mirroring the Python _modifiers helper.
func modifiers(get func(name string) bool) int {
	result := 0
	if get("ctrl") {
		result |= int(input.ModCtrl)
	}
	if get("alt") {
		result |= int(input.ModAlt)
	}
	if get("shift") {
		result |= int(input.ModShift)
	}
	return result
}
