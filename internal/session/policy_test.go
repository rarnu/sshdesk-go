package session

import (
	"image"
	"testing"
	"time"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/render"
)

func TestNextAutoRenderScale(t *testing.T) {
	if got := NextAutoRenderScale(1.0, 260.0, 5.0); got != 0.75 {
		t.Errorf("got %v, want 0.75", got)
	}
	if got := NextAutoRenderScale(0.75, 40.0, 4.0); got != 0.81 {
		t.Errorf("got %v, want 0.81", got)
	}
	if got := NextAutoRenderScale(0.5, 500.0, 50.0); got != 0.5 {
		t.Errorf("got %v, want 0.5 (floor)", got)
	}
	if got := NextAutoRenderScale(1.0, 10.0, 2.0); got != 1.0 {
		t.Errorf("got %v, want 1.0 (no downgrade without backpressure)", got)
	}
}

func TestTerminalBackpressureCapsRefreshRate(t *testing.T) {
	if got := LimitForTerminalBackpressure(60.0, 0.0, 5.0); got != 60.0 {
		t.Errorf("got %v, want 60", got)
	}
	if got := LimitForTerminalBackpressure(60.0, 150.0, 5.0); got != 30.0 {
		t.Errorf("got %v, want 30", got)
	}
	if got := LimitForTerminalBackpressure(60.0, 0.0, 25.0); got != 20.0 {
		t.Errorf("got %v, want 20", got)
	}
	if got := LimitForTerminalBackpressure(60.0, 600.0, 0.0); got != 10.0 {
		t.Errorf("got %v, want 10", got)
	}
}

func TestPendingLatencyProbeCapsRefreshRate(t *testing.T) {
	direct := NewDirectSession(nil, nil, render.Capabilities{
		Term:     "test",
		Color:    render.Color256,
		Mouse:    true,
		SGRMouse: true,
		Unicode:  true,
	}, nil, nil)
	direct.activeFPS = 60.0
	direct.latencyProbeNs.Store(time.Now().UnixNano() - 600_000_000)
	now := monotonicNow()
	if got := direct.refreshRate(now, now); got > 10.0 {
		t.Errorf("got %v, want <= 10 with a 600ms pending probe", got)
	}
}

func TestIdenticalCaptureDigestReusesRenderedState(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 9))
	same := &capture.Frame{Image: img, CapturedNs: 1, ContentDigest: []byte("same")}
	changed := &capture.Frame{Image: img, CapturedNs: 2, ContentDigest: []byte("changed")}
	unknown := &capture.Frame{Image: img, CapturedNs: 3}
	if !CanReuseRenderedFrame(true, []byte("same"), same) {
		t.Error("identical digest must reuse")
	}
	if CanReuseRenderedFrame(true, []byte("same"), changed) {
		t.Error("changed digest must not reuse")
	}
	if CanReuseRenderedFrame(true, []byte("same"), unknown) {
		t.Error("missing digest must not reuse")
	}
	if CanReuseRenderedFrame(false, []byte("same"), same) {
		t.Error("no previous frame must not reuse")
	}
}
