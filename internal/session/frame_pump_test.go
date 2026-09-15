package session

import (
	"errors"
	"image"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rarnu/sshdesk-go/internal/capture"
)

type fastCapture struct {
	mu     sync.Mutex
	target image.Point
	calls  int
	rates  []float64
}

func newFastCapture() *fastCapture {
	return &fastCapture{target: image.Point{X: 16, Y: 9}}
}

func (c *fastCapture) SetTargetSize(width, height int) error {
	c.mu.Lock()
	c.target = image.Point{X: width, Y: height}
	c.mu.Unlock()
	return nil
}

func (c *fastCapture) Size() (int, int) { return 1920, 1080 }

func (c *fastCapture) SetFrameRate(fps float64) error {
	c.mu.Lock()
	c.rates = append(c.rates, fps)
	c.mu.Unlock()
	return nil
}

func (c *fastCapture) CursorPosition() (int, int, bool) { return 0, 0, false }

func (c *fastCapture) Close() {}

func (c *fastCapture) Capture() (*capture.Frame, error) {
	c.mu.Lock()
	c.calls++
	target := c.target
	color := uint8(c.calls % 255)
	c.mu.Unlock()
	img := image.NewRGBA(image.Rect(0, 0, target.X, target.Y))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = color
		img.Pix[i+1] = color
		img.Pix[i+2] = color
		img.Pix[i+3] = 0xFF
	}
	return &capture.Frame{
		Image:         img,
		CapturedNs:    time.Now().UnixNano(),
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, nil
}

type blockingCapture struct {
	*fastCapture
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingCapture() *blockingCapture {
	return &blockingCapture{
		fastCapture: newFastCapture(),
		started:     make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (c *blockingCapture) Capture() (*capture.Frame, error) {
	c.mu.Lock()
	target := c.target
	calls := c.calls
	c.calls++
	c.mu.Unlock()
	if calls == 0 {
		c.once.Do(func() { close(c.started) })
		select {
		case <-c.release:
		case <-time.After(time.Second):
		}
	}
	return &capture.Frame{
		Image:         image.NewRGBA(image.Rect(0, 0, target.X, target.Y)),
		CapturedNs:    time.Now().UnixNano(),
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, nil
}

type failingCapture struct {
	*fastCapture
}

func (c *failingCapture) Capture() (*capture.Frame, error) {
	return nil, errors.New("PipeWire stream ended")
}

func startPump(t *testing.T, backend capture.ScreenCapture, fps float64) *LatestFramePump {
	t.Helper()
	pump, err := NewLatestFramePump(backend, fps)
	if err != nil {
		t.Fatal(err)
	}
	if err := pump.SetTargetSize(16, 9); err != nil {
		t.Fatal(err)
	}
	if err := pump.Start(); err != nil {
		t.Fatal(err)
	}
	return pump
}

func TestFramePumpKeepsLatestInsteadOfQueueing(t *testing.T) {
	backend := newFastCapture()
	pump := startPump(t, backend, 120)
	defer pump.Close()
	first, err := pump.LatestAfter(0, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	if first == nil {
		t.Fatal("no first frame")
	}
	deadline := time.Now().Add(time.Second)
	for pump.CapturedFrames < first.Sequence+2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	newest, err := pump.LatestAfter(first.Sequence, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	if newest == nil {
		t.Fatal("no newer frame")
	}
	if newest.Sequence <= first.Sequence+1 {
		t.Errorf("sequence = %d, want > %d", newest.Sequence, first.Sequence+1)
	}
	if pump.DroppedFrames == 0 {
		t.Error("unconsumed frames must be counted as dropped")
	}
}

func TestResizeDiscardsInFlightOldGeometry(t *testing.T) {
	backend := newBlockingCapture()
	pump := startPump(t, backend, 120)
	defer pump.Close()
	defer close(backend.release)
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("capture did not start")
	}
	if err := pump.SetTargetSize(32, 18); err != nil {
		t.Fatal(err)
	}
	backend.release <- struct{}{}
	frame, err := pump.LatestAfter(0, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	if frame == nil {
		t.Fatal("no frame after resize")
	}
	if got := frame.Frame.Image.Rect.Dx(); got != 32 {
		t.Errorf("width = %d, want 32 (new geometry)", got)
	}
	if got := frame.Frame.Image.Rect.Dy(); got != 18 {
		t.Errorf("height = %d, want 18 (new geometry)", got)
	}
}

func TestFrameRateChangeReachesCaptureBackend(t *testing.T) {
	backend := newFastCapture()
	pump := startPump(t, backend, 120)
	defer pump.Close()
	if _, err := pump.LatestAfter(0, 1.0); err != nil {
		t.Fatal(err)
	}
	if err := pump.SetFramesPerSecond(10); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	seen := func(rate float64) bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		for _, value := range backend.rates {
			if value == rate {
				return true
			}
		}
		return false
	}
	for !seen(10.0) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !seen(120.0) {
		t.Error("initial 120 fps must reach the backend")
	}
	if !seen(10.0) {
		t.Error("updated 10 fps must reach the backend")
	}
}

func TestCaptureWorkerPreservesBackendErrorDetail(t *testing.T) {
	pump := startPump(t, &failingCapture{newFastCapture()}, 60)
	defer pump.Close()
	_, err := pump.LatestAfter(0, 1.0)
	if err == nil {
		t.Fatal("backend failure must propagate")
	}
	if !strings.Contains(err.Error(), "PipeWire stream ended") {
		t.Errorf("error = %v, want backend detail", err)
	}
}

func TestFramePumpValidatesBounds(t *testing.T) {
	if _, err := NewLatestFramePump(newFastCapture(), 0.1); err == nil {
		t.Error("fps below 0.5 must fail")
	}
	pump, err := NewLatestFramePump(newFastCapture(), 60)
	if err != nil {
		t.Fatal(err)
	}
	if err := pump.SetTargetSize(0, 9); err == nil {
		t.Error("zero width must fail")
	}
	if err := pump.SetTargetSize(16385, 9); err == nil {
		t.Error("width above 16384 must fail")
	}
	if err := pump.Start(); err == nil {
		t.Error("start without target size must fail")
	}
}
