// Package wayland implements Wayland capture through the compositor's
// non-interactive screenshot helper (grim, gnome-screenshot, or spectacle),
// mirroring the Python WaylandCapture. Everything runs through fixed argument
// vectors and PNG decoding, so the package is platform-neutral and fully
// testable without a Wayland session.
package wayland

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/xshm"
)

// grabTimeout mirrors the Python subprocess timeout.
const grabTimeout = 5 * time.Second

// SelectBackend mirrors the Python helper-selection cascade: the desktop's
// own tool wins, then grim, then whatever is installed.
func SelectBackend(desktop string, available func(string) bool) (string, error) {
	desktop = strings.ToLower(desktop)
	if (strings.Contains(desktop, "gnome") || strings.Contains(desktop, "unity")) && available("gnome-screenshot") {
		return "gnome-screenshot", nil
	}
	if (strings.Contains(desktop, "kde") || strings.Contains(desktop, "plasma")) && available("spectacle") {
		return "spectacle", nil
	}
	if available("grim") {
		return "grim", nil
	}
	if available("gnome-screenshot") {
		return "gnome-screenshot", nil
	}
	if available("spectacle") {
		return "spectacle", nil
	}
	return "", errors.New("Wayland capture needs grim (wlroots), gnome-screenshot (GNOME), or spectacle (KDE Plasma)")
}

// Command is the fixed argument vector for one screenshot; grim writes PNG
// to stdout while the desktop tools write to outputPath.
func Command(backend, outputPath string) []string {
	switch backend {
	case "grim":
		return []string{"grim", "-c", "-t", "png", "-"}
	case "gnome-screenshot":
		return []string{"gnome-screenshot", "-f", outputPath}
	default:
		return []string{"spectacle", "-b", "-n", "-o", outputPath}
	}
}

// runResult is one finished helper invocation.
type runResult struct {
	stdout   []byte
	stderr   []byte
	exitCode int
}

// runner executes one helper command; it is a field seam for tests.
type runner func(argv []string, timeout time.Duration) (runResult, error)

// execRunner spawns the helper with a null stdin and captured output.
func execRunner(argv []string, timeout time.Duration) (runResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return runResult{}, fmt.Errorf("%s Wayland capture timed out after %s seconds",
			argv[0], strconv.FormatFloat(timeout.Seconds(), 'g', -1, 64))
	}
	result := runResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	if exit, ok := err.(*exec.ExitError); ok {
		result.exitCode = exit.ExitCode()
		return result, nil
	}
	if err != nil {
		return runResult{}, err
	}
	return result, nil
}

// Capture mirrors the Python WaylandCapture.
type Capture struct {
	Backend string

	run           runner
	targetWidth   int
	targetHeight  int
	desktopWidth  int
	desktopHeight int
}

var _ capture.ScreenCapture = (*Capture)(nil)

// New selects the screenshot helper and grabs one frame to learn the
// desktop size, like the Python constructor.
func New() (*Capture, error) {
	if os.Getenv("WAYLAND_DISPLAY") == "" {
		return nil, errors.New("WAYLAND_DISPLAY is not set; Wayland capture is unavailable")
	}
	backend, err := SelectBackend(os.Getenv("XDG_CURRENT_DESKTOP"), func(name string) bool {
		_, err := exec.LookPath(name)
		return err == nil
	})
	if err != nil {
		return nil, err
	}
	c := &Capture{Backend: backend, run: execRunner}
	first, err := c.grab()
	if err != nil {
		return nil, err
	}
	c.desktopWidth = first.Rect.Dx()
	c.desktopHeight = first.Rect.Dy()
	return c, nil
}

// BackendName mirrors the Python class name used in status output.
func (c *Capture) BackendName() string { return "WaylandCapture" }

func (c *Capture) runnerFunc() runner {
	if c.run != nil {
		return c.run
	}
	return execRunner
}

// helperError mirrors the Python RuntimeError for a failed helper.
func (c *Capture) helperError(result runResult) error {
	detail := strings.TrimSpace(string(bytes.ToValidUTF8(result.stderr, []byte("�"))))
	if detail == "" {
		detail = "unknown error"
	}
	return fmt.Errorf("%s Wayland capture failed: %s", c.Backend, detail)
}

// grab captures one desktop screenshot as an RGB image.
func (c *Capture) grab() (*image.RGBA, error) {
	run := c.runnerFunc()
	if c.Backend == "grim" {
		result, err := run(Command("grim", ""), grabTimeout)
		if err != nil {
			return nil, err
		}
		if result.exitCode != 0 {
			return nil, c.helperError(result)
		}
		return decodePNG(bytes.NewReader(result.stdout))
	}

	file, err := os.CreateTemp("", "sshdesk-capture-*.png")
	if err != nil {
		return nil, err
	}
	path := file.Name()
	file.Close()
	defer os.Remove(path)

	result, err := run(Command(c.Backend, path), grabTimeout)
	if err != nil {
		return nil, err
	}
	if result.exitCode != 0 {
		return nil, c.helperError(result)
	}
	opened, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	return decodePNG(opened)
}

// decodePNG decodes one PNG screenshot into a packed RGBA image (the Go
// stand-in for Pillow's convert("RGB")).
func decodePNG(reader io.Reader) (*image.RGBA, error) {
	decoded, err := png.Decode(reader)
	if err != nil {
		return nil, fmt.Errorf("cannot decode the Wayland capture PNG: %v", err)
	}
	if rgba, ok := decoded.(*image.RGBA); ok {
		return rgba, nil
	}
	bounds := decoded.Bounds()
	rgba := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(rgba, rgba.Rect, decoded, bounds.Min, draw.Src)
	return rgba, nil
}

// Capture grabs one frame, scales it to the target size, and fingerprints it.
func (c *Capture) Capture() (*capture.Frame, error) {
	img, err := c.grab()
	if err != nil {
		return nil, err
	}
	capturedNs := time.Now().UnixNano()
	c.desktopWidth = img.Rect.Dx()
	c.desktopHeight = img.Rect.Dy()
	if c.targetWidth > 0 && c.targetHeight > 0 &&
		(c.desktopWidth != c.targetWidth || c.desktopHeight != c.targetHeight) {
		img = xshm.Scale(img, c.targetWidth, c.targetHeight)
	}
	return &capture.Frame{
		Image:         img,
		CapturedNs:    capturedNs,
		DesktopWidth:  c.desktopWidth,
		DesktopHeight: c.desktopHeight,
		ContentDigest: capture.DigestPixels(rgb24(img)),
	}, nil
}

// Size reports the desktop size learned from the last grab.
func (c *Capture) Size() (int, int) { return c.desktopWidth, c.desktopHeight }

// CursorPosition is unavailable through screenshot helpers.
func (c *Capture) CursorPosition() (int, int, bool) { return 0, 0, false }

// SetTargetSize requests a capture image size while retaining desktop
// coordinates.
func (c *Capture) SetTargetSize(width, height int) error {
	if err := capture.ValidateTargetSize(width, height); err != nil {
		return err
	}
	c.targetWidth, c.targetHeight = width, height
	return nil
}

// SetFrameRate is a no-op like the Python base class; screenshot helpers
// cannot pace themselves.
func (c *Capture) SetFrameRate(framesPerSecond float64) error { return nil }

// Close releases nothing; helpers are per-capture child processes.
func (c *Capture) Close() {}

// rgb24 packs the image into RGB24 bytes for the content digest.
func rgb24(img *image.RGBA) []byte {
	width := img.Rect.Dx()
	height := img.Rect.Dy()
	out := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			s := img.PixOffset(x, y)
			d := (y*width + x) * 3
			out[d] = img.Pix[s]
			out[d+1] = img.Pix[s+1]
			out[d+2] = img.Pix[s+2]
		}
	}
	return out
}
