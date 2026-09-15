package gnome

import (
	"errors"
	"fmt"
	"image"
	"os/exec"
	"sync"
	"time"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/capture/xshm"
	"github.com/rarnu/sshdesk-go/internal/input"
	"github.com/rarnu/sshdesk-go/internal/input/mutter"
)

const (
	callTimeout = 5 * time.Second
	stopTimeout = time.Second
)

// streamTimeout is a package variable so tests can shrink the node wait.
var streamTimeout = 5 * time.Second

// bus is the D-Bus seam for the GNOME capture; the production implementation
// wraps godbus, tests inject a recorder.
type bus interface {
	currentState() (DisplayState, error)
	call(destination, path, iface, method string, timeout time.Duration, args ...any) ([]any, error)
	subscribe(path, iface, member string, handler func(node uint32)) (unsubscribe func(), err error)
	sessionCaller(path string) mutter.Caller
	close()
}

// Capture mirrors the Python GnomeScreenCastCapture: one persistent Mutter
// desktop stream drained by a gst-launch child process.
type Capture struct {
	mu            sync.Mutex
	bus           bus
	gstExecutable string

	area              Rectangle
	desktopWidth      int
	desktopHeight     int
	targetWidth       int
	targetHeight      int
	framesPerSecond   float64
	remoteSessionPath string
	screenSessionPath string
	streamPath        string
	unsubscribe       func()
	pipewireNode      uint32
	cursorX           int
	cursorY           int
	hasCursor         bool
	stream            frameStream
	newStream         func(node uint32, width, height int) frameStream
	closed            bool
}

var _ capture.ScreenCapture = (*Capture)(nil)

// New connects to the session bus, resolves the desktop area, and opens the
// persistent screen stream.
func New() (*Capture, error) {
	executable, err := exec.LookPath("gst-launch-1.0")
	if err != nil {
		return nil, errors.New("GNOME streaming needs the GStreamer gst-launch-1.0 tool and PipeWire plugin")
	}
	sessionBus, err := connectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("GNOME capture needs a D-Bus session bus: %v", err)
	}
	c := NewForBus(sessionBus, executable)
	if err := c.open(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

// NewForBus builds a capture on an injected bus (tests and New share it).
func NewForBus(sessionBus bus, gstExecutable string) *Capture {
	c := &Capture{
		bus:             sessionBus,
		gstExecutable:   gstExecutable,
		framesPerSecond: 60.0,
	}
	c.newStream = func(node uint32, width, height int) frameStream {
		return newStreamCapture(gstExecutable, node, width, height)
	}
	return c
}

// open resolves the desktop area and opens the stream, like the Python
// constructor body.
func (c *Capture) open() error {
	area, err := c.desktopArea()
	if err != nil {
		return err
	}
	c.area = area
	c.desktopWidth = area.Width
	c.desktopHeight = area.Height
	return c.openStream(area)
}

// BackendName mirrors the Python class name used in status output.
func (c *Capture) BackendName() string { return "GnomeScreenCastCapture" }

// desktopArea mirrors _desktop_area.
func (c *Capture) desktopArea() (Rectangle, error) {
	state, err := c.bus.currentState()
	if err != nil {
		return Rectangle{}, err
	}
	return DesktopArea(state)
}

// firstString unwraps the first reply value as a string (D-Bus object paths
// and SessionId values arrive as plain strings through the seam).
func firstString(body []any) (string, error) {
	if len(body) == 0 {
		return "", errors.New("GNOME returned an empty reply")
	}
	value, ok := body[0].(string)
	if !ok {
		return "", fmt.Errorf("GNOME returned an unexpected reply value %T", body[0])
	}
	return value, nil
}

// openStream mirrors _open_stream: remote session, linked screen session,
// RecordArea with a hidden cursor, PipeWireStreamAdded subscription, Start,
// and a five second wait for the node id.
func (c *Capture) openStream(area Rectangle) error {
	body, err := c.bus.call(RemoteName, RemoteRoot, RemoteRootInterface, "CreateSession", callTimeout)
	if err != nil {
		return err
	}
	remotePath, err := firstString(body)
	if err != nil {
		return err
	}
	c.remoteSessionPath = remotePath

	body, err = c.bus.call(RemoteName, remotePath, PropertiesInterface, "Get", callTimeout,
		RemoteSessionInterface, "SessionId")
	if err != nil {
		return err
	}
	sessionID, err := firstString(body)
	if err != nil {
		return err
	}

	body, err = c.bus.call(ScreencastName, ScreencastRoot, ScreencastRootInterface, "CreateSession", callTimeout,
		map[string]any{"remote-desktop-session-id": sessionID})
	if err != nil {
		return err
	}
	screenPath, err := firstString(body)
	if err != nil {
		return err
	}
	c.screenSessionPath = screenPath

	body, err = c.bus.call(ScreencastName, screenPath, ScreencastSessionInterface, "RecordArea", callTimeout,
		int32(area.X), int32(area.Y), int32(area.Width), int32(area.Height),
		// Mutter cursor mode 0 hides it from the video stream. SSHDESK sends
		// its own tiny cursor placement updates, so pointer movement must not
		// dirty and re-encode the desktop framebuffer.
		map[string]any{"cursor-mode": uint32(0)})
	if err != nil {
		return err
	}
	streamPath, err := firstString(body)
	if err != nil {
		return err
	}
	c.streamPath = streamPath

	unsubscribe, err := c.bus.subscribe(streamPath, ScreencastStreamInterface, "PipeWireStreamAdded",
		func(node uint32) {
			c.mu.Lock()
			c.pipewireNode = node
			c.mu.Unlock()
		})
	if err != nil {
		return err
	}
	c.unsubscribe = unsubscribe

	if _, err := c.bus.call(RemoteName, remotePath, RemoteSessionInterface, "Start", callTimeout); err != nil {
		return err
	}

	deadline := time.Now().Add(streamTimeout)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		node := c.pipewireNode
		c.mu.Unlock()
		if node != 0 {
			return nil
		}
		time.Sleep(5 * time.Millisecond)
	}
	return errors.New("GNOME did not publish its PipeWire desktop stream")
}

// Size reports the desktop size.
func (c *Capture) Size() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.desktopWidth, c.desktopHeight
}

// CursorPosition reports the last pointer position injected through the
// linked Mutter input backend.
func (c *Capture) CursorPosition() (int, int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cursorX, c.cursorY, c.hasCursor
}

func (c *Capture) setCursorPosition(x, y int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cursorX, c.cursorY = x, y
	c.hasCursor = true
}

// SetTargetSize clamps the renderer target to the desktop. The PipeWire
// stream keeps running at the native desktop size and Capture scales
// frames down, so moving the target never restarts the capture process.
func (c *Capture) SetTargetSize(width, height int) error {
	if err := capture.ValidateTargetSize(width, height); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.targetWidth = min(width, c.desktopWidth)
	c.targetHeight = min(height, c.desktopHeight)
	return nil
}

// SetFrameRate stores the pacing request; the continuous PipeWire stream
// cannot pace itself, exactly like the Python appsink version.
func (c *Capture) SetFrameRate(framesPerSecond float64) error {
	if err := capture.ValidateFrameRate(framesPerSecond); err != nil {
		return err
	}
	c.framesPerSecond = framesPerSecond
	return nil
}

// startStream lazily starts the gst-launch child at the native desktop
// size. The pipeline is resolution-independent of the renderer target, so
// the child process is started once and survives SetTargetSize calls.
func (c *Capture) startStream() error {
	if c.stream != nil {
		return nil
	}
	if c.pipewireNode == 0 {
		return errors.New("the GNOME PipeWire stream is not available")
	}
	c.stream = c.newStream(c.pipewireNode, c.desktopWidth, c.desktopHeight)
	return nil
}

func (c *Capture) stopStream() {
	if c.stream != nil {
		c.stream.Close()
		c.stream = nil
	}
}

// Capture reads one frame, rebuilding the stream once after a failure like
// the Python attempt loop.
func (c *Capture) Capture() (*capture.Frame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("GNOME capture is closed")
	}
	var frame streamFrame
	var firstError error
	for attempt := 0; attempt < 2; attempt++ {
		if err := c.startStream(); err != nil {
			return nil, err
		}
		var err error
		frame, err = c.stream.Capture()
		if err == nil {
			break
		}
		if firstError == nil {
			firstError = err
		}
		if attempt == 0 {
			// Recover from a transient PipeWire pause or pipeline error
			// without dropping the authenticated SSH session.
			c.stopStream()
		}
		if attempt == 1 {
			return nil, fmt.Errorf("GNOME PipeWire capture failed: %v", firstError)
		}
	}
	img := rgb24ToRGBA(frame.RGB, c.desktopWidth, c.desktopHeight)
	if c.targetWidth > 0 && c.targetHeight > 0 &&
		(c.targetWidth != c.desktopWidth || c.targetHeight != c.desktopHeight) {
		img = xshm.Scale(img, c.targetWidth, c.targetHeight)
	}
	return &capture.Frame{
		Image:         img,
		CapturedNs:    frame.CapturedNs,
		DesktopWidth:  c.desktopWidth,
		DesktopHeight: c.desktopHeight,
		ContentDigest: frame.ContentDigest,
	}, nil
}

// CreateInputBackend links a MutterInput to the same remote desktop
// session, mirroring the Python create_input_backend.
func (c *Capture) CreateInputBackend() (input.Backend, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.remoteSessionPath == "" || c.streamPath == "" {
		return nil, errors.New("the GNOME remote desktop session is unavailable")
	}
	size := func() (int, int) { return c.Size() }
	return mutter.New(
		c.bus.sessionCaller(c.remoteSessionPath),
		c.remoteSessionPath,
		c.streamPath,
		size,
		c.setCursorPosition,
	), nil
}

// Close stops the pipeline and both D-Bus sessions in the Python order,
// then releases the private bus connection (godbus connections are owned,
// unlike the shared Gio bus the Python version leaves open).
func (c *Capture) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.stopStream()
	if c.unsubscribe != nil {
		c.unsubscribe()
		c.unsubscribe = nil
	}
	screenPath := c.screenSessionPath
	c.screenSessionPath = ""
	remotePath := c.remoteSessionPath
	c.remoteSessionPath = ""
	c.streamPath = ""
	c.pipewireNode = 0
	c.hasCursor = false
	sessionBus := c.bus
	c.mu.Unlock()

	if screenPath != "" {
		_, _ = sessionBus.call(ScreencastName, screenPath, ScreencastSessionInterface, "Stop", stopTimeout)
	}
	if remotePath != "" {
		_, _ = sessionBus.call(RemoteName, remotePath, RemoteSessionInterface, "Stop", stopTimeout)
	}
	sessionBus.close()
}

// rgb24ToRGBA expands packed RGB24 pixels into an RGBA image.
func rgb24ToRGBA(rgb []byte, width, height int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < width*height && i*3+2 < len(rgb); i++ {
		s, d := i*3, i*4
		img.Pix[d] = rgb[s]
		img.Pix[d+1] = rgb[s+1]
		img.Pix[d+2] = rgb[s+2]
		img.Pix[d+3] = 0xFF
	}
	return img
}
