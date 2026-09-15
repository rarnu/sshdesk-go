//go:build linux

package x11

import (
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/capture/ffmpeg"
	"github.com/rarnu/sshdesk-go/internal/capture/xshm"
)

const (
	zpixmap          = 2
	allPlanes        = 0xFFFFFFFF
	geometryInterval = time.Second
)

// Capture is the composite X11 root-window capture, mirroring the Python
// X11Capture: an FFmpeg stream tier, an MIT-SHM tier, and an xgb GetImage
// fallback (standing in for Pillow's ImageGrab).
type Capture struct {
	mu                sync.Mutex
	displayName       string
	conn              *xgb.Conn
	root              xproto.Window
	layout            xshm.PixelLayout
	desktopWidth      int
	desktopHeight     int
	targetWidth       int
	targetHeight      int
	framesPerSecond   float64
	ffmpegExecutable  string
	nextGeometryCheck time.Time
	chain             chain
}

var _ capture.ScreenCapture = (*Capture)(nil)

// NewCapture opens the display and prepares the tier chain. Tiers start
// lazily on the first Capture.
func NewCapture(displayName string) (*Capture, error) {
	if displayName == "" {
		displayName = os.Getenv("DISPLAY")
	}
	if displayName == "" {
		return nil, errors.New("DISPLAY is not set; X11 capture is unavailable")
	}
	conn, err := xgb.NewConnDisplay(displayName)
	if err != nil {
		return nil, fmt.Errorf("cannot open X11 display %q", displayName)
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	geometry, err := xproto.GetGeometry(conn, xproto.Drawable(screen.Root)).Reply()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("cannot query the X11 root geometry: %v", err)
	}
	layout, err := xshm.RootLayout(setup, screen)
	if err != nil {
		conn.Close()
		return nil, err
	}

	backend := strings.ToLower(strings.TrimSpace(os.Getenv("SSHDESK_X11_CAPTURE")))
	if backend == "" {
		backend = "auto"
	}
	switch backend {
	case "auto", "ffmpeg", "xshm", "pillow", "xcb":
	default:
		conn.Close()
		return nil, errors.New("SSHDESK_X11_CAPTURE must be auto, ffmpeg, xshm, or pillow")
	}
	ffmpegExecutable, _ := exec.LookPath("ffmpeg")
	if backend == "ffmpeg" && ffmpegExecutable == "" {
		conn.Close()
		return nil, errors.New("FFmpeg capture was requested but ffmpeg is not installed")
	}

	c := &Capture{
		displayName:      displayName,
		conn:             conn,
		root:             screen.Root,
		layout:           layout,
		desktopWidth:     int(geometry.Width),
		desktopHeight:    int(geometry.Height),
		framesPerSecond:  60.0,
		ffmpegExecutable: ffmpegExecutable,
	}
	c.chain.backend = backend
	c.chain.ffmpegDisabled = backend == "xshm" || backend == "pillow" || backend == "xcb" || ffmpegExecutable == ""
	c.chain.sharedDisabled = backend == "pillow" || backend == "xcb"
	c.chain.newFFmpeg = c.newFFmpegTier
	c.chain.newShared = c.newSharedTier
	c.chain.fallback = c.fallbackCapture
	return c, nil
}

// BackendName mirrors the Python class name used in status output.
func (c *Capture) BackendName() string { return "X11Capture" }

// ffmpegTier adapts the FFmpeg stream capture to the chain tier interface.
type ffmpegTier struct{ cap *ffmpeg.Capture }

func (t *ffmpegTier) capture() (Frame, error) {
	frame, err := t.cap.Capture()
	if err != nil {
		return Frame{}, err
	}
	return Frame{
		Image:         rgb24ToRGBA(frame.RGB, t.cap.TargetWidth, t.cap.TargetHeight),
		CapturedNs:    frame.CapturedNs,
		ContentDigest: frame.ContentDigest,
	}, nil
}

func (t *ffmpegTier) close()                          { t.cap.Close() }
func (t *ffmpegTier) setTargetSize(width, height int) { t.cap.SetTargetSize(width, height) }
func (t *ffmpegTier) setFrameRate(fps float64) error  { return t.cap.SetFrameRate(fps) }

// xshmTier adapts the MIT-SHM capture, resolving the target size per frame.
type xshmTier struct {
	cap    *xshm.Capture
	target func() (int, int)
}

func (t *xshmTier) capture() (Frame, error) {
	width, height := t.target()
	frame, err := t.cap.Capture(width, height)
	if err != nil {
		return Frame{}, err
	}
	return Frame{Image: frame.Image, CapturedNs: frame.CapturedNs, ContentDigest: frame.ContentDigest}, nil
}

func (t *xshmTier) close() { t.cap.Close() }

func (c *Capture) newFFmpegTier() (tier, error) {
	capTier, err := ffmpeg.New(c.ffmpegExecutable, c.displayName, c.desktopWidth, c.desktopHeight)
	if err != nil {
		return nil, err
	}
	width, height := c.targetSize()
	capTier.SetTargetSize(width, height)
	if err := capTier.SetFrameRate(c.framesPerSecond); err != nil {
		capTier.Close()
		return nil, err
	}
	return &ffmpegTier{cap: capTier}, nil
}

func (c *Capture) newSharedTier() (tier, error) {
	capTier, err := xshm.New(c.displayName, c.desktopWidth, c.desktopHeight)
	if err != nil {
		return nil, err
	}
	return &xshmTier{cap: capTier, target: c.targetSize}, nil
}

// targetSize is the renderer target clamped to the desktop, like the Python
// _target_size or _desktop_size fallback.
func (c *Capture) targetSize() (int, int) {
	if c.targetWidth > 0 && c.targetHeight > 0 {
		return c.targetWidth, c.targetHeight
	}
	return c.desktopWidth, c.desktopHeight
}

// fallbackCapture grabs the full desktop through the core GetImage request,
// the xgb stand-in for Pillow's ImageGrab fallback.
func (c *Capture) fallbackCapture() (Frame, error) {
	reply, err := xproto.GetImage(c.conn, zpixmap, xproto.Drawable(c.root), 0, 0,
		uint16(c.desktopWidth), uint16(c.desktopHeight), allPlanes).Reply()
	if err != nil {
		return Frame{}, fmt.Errorf("X11 GetImage fallback failed: %v", err)
	}
	capturedNs := time.Now().UnixNano()
	if err := c.layout.Validate(); err != nil {
		return Frame{}, err
	}
	width, height := c.desktopWidth, c.desktopHeight
	stride := width * 4
	if len(reply.Data) != stride*height {
		if len(reply.Data)%height != 0 || len(reply.Data)/height < width*4 {
			return Frame{}, fmt.Errorf("X11 GetImage fallback returned %d bytes for a %dx%d desktop", len(reply.Data), width, height)
		}
		stride = len(reply.Data) / height
	}
	img, err := xshm.BGRXToRGB(reply.Data, width, height, stride)
	if err != nil {
		return Frame{}, err
	}
	targetWidth, targetHeight := c.targetSize()
	img = xshm.Scale(img, targetWidth, targetHeight)
	return Frame{
		Image:         img,
		CapturedNs:    capturedNs,
		ContentDigest: capture.DigestPixels(rgb24(img)),
	}, nil
}

// refreshGeometry polls the root size once per second; a resize restarts the
// stream tiers so they pick up the new desktop size.
func (c *Capture) refreshGeometry() {
	now := time.Now()
	if now.Before(c.nextGeometryCheck) {
		return
	}
	c.nextGeometryCheck = now.Add(geometryInterval)
	geometry, err := xproto.GetGeometry(c.conn, xproto.Drawable(c.root)).Reply()
	if err != nil {
		return
	}
	width, height := int(geometry.Width), int(geometry.Height)
	if width != c.desktopWidth || height != c.desktopHeight {
		c.desktopWidth, c.desktopHeight = width, height
		c.chain.close()
	}
}

// Capture returns one frame at target size through the tier chain.
func (c *Capture) Capture() (*capture.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshGeometry()
	frame, err := c.chain.capture()
	if err != nil {
		return nil, err
	}
	return &capture.Frame{
		Image:         frame.Image,
		CapturedNs:    frame.CapturedNs,
		DesktopWidth:  c.desktopWidth,
		DesktopHeight: c.desktopHeight,
		ContentDigest: frame.ContentDigest,
	}, nil
}

// Size reports the desktop size, polling geometry like the Python size().
func (c *Capture) Size() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshGeometry()
	return c.desktopWidth, c.desktopHeight
}

// CursorPosition reports the pointer location in root coordinates.
func (c *Capture) CursorPosition() (int, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reply, err := xproto.QueryPointer(c.conn, c.root).Reply()
	if err != nil {
		return 0, 0, false
	}
	return int(reply.RootX), int(reply.RootY), true
}

// SetTargetSize clamps the renderer target to the desktop and restarts the
// FFmpeg stream when it is live.
func (c *Capture) SetTargetSize(width, height int) error {
	if err := capture.ValidateTargetSize(width, height); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.targetWidth = min(width, c.desktopWidth)
	c.targetHeight = min(height, c.desktopHeight)
	if live, ok := c.chain.ffmpeg.(interface{ setTargetSize(int, int) }); ok {
		live.setTargetSize(c.targetWidth, c.targetHeight)
	}
	return nil
}

// SetFrameRate re-paces the FFmpeg stream tier.
func (c *Capture) SetFrameRate(framesPerSecond float64) error {
	if err := capture.ValidateFrameRate(framesPerSecond); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.framesPerSecond = framesPerSecond
	if live, ok := c.chain.ffmpeg.(interface{ setFrameRate(float64) error }); ok {
		return live.setFrameRate(framesPerSecond)
	}
	return nil
}

// Close shuts down every tier and the display connection.
func (c *Capture) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chain.close()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// rgb24ToRGBA expands packed RGB24 pixels into an RGBA image.
func rgb24ToRGBA(rgb []byte, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height; i++ {
		s, d := i*3, i*4
		img.Pix[d] = rgb[s]
		img.Pix[d+1] = rgb[s+1]
		img.Pix[d+2] = rgb[s+2]
		img.Pix[d+3] = 0xFF
	}
	return img
}

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
