package session

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rarnu/sshdesk-go/internal/capture"
)

// CapturedFrame couples a desktop frame with its pump sequence number.
type CapturedFrame struct {
	Frame     *capture.Frame
	Sequence  int
	CaptureMs float64
}

// LatestFramePump captures on a paced worker and retains only the newest
// frame. A slow terminal or network must never create a stale framebuffer
// queue; overwriting an unconsumed frame trades work for lower latency.
type LatestFramePump struct {
	capture capture.ScreenCapture

	mu               sync.Mutex
	framesPerSecond  float64
	rateGeneration   int
	targetSize       *imageSize
	targetGeneration int
	latest           *CapturedFrame
	sequence         int
	delivered        int
	stop             bool
	err              error
	done             chan struct{}
	started          bool
	notify           chan struct{}

	CapturedFrames int
	DroppedFrames  int
}

type imageSize struct {
	width  int
	height int
}

func boundedFPS(value float64) (float64, error) {
	if value < 0.5 || value > 120.0 {
		return 0, errors.New("capture FPS must be between 0.5 and 120")
	}
	return value, nil
}

func NewLatestFramePump(captureBackend capture.ScreenCapture, framesPerSecond float64) (*LatestFramePump, error) {
	fps, err := boundedFPS(framesPerSecond)
	if err != nil {
		return nil, err
	}
	return &LatestFramePump{
		capture:         captureBackend,
		framesPerSecond: fps,
		notify:          make(chan struct{}, 1),
	}, nil
}

// ErrPumpStarted reports a double start.
var ErrPumpStarted = errors.New("frame pump is already started")

// ErrPumpNoTarget reports a start without a configured target size.
var ErrPumpNoTarget = errors.New("frame pump target size is not configured")

func (p *LatestFramePump) Start() error {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return ErrPumpStarted
	}
	if p.targetSize == nil {
		p.mu.Unlock()
		return ErrPumpNoTarget
	}
	p.started = true
	p.done = make(chan struct{})
	p.mu.Unlock()
	go p.run()
	return nil
}

func (p *LatestFramePump) signal() {
	select {
	case p.notify <- struct{}{}:
	default:
	}
}

func (p *LatestFramePump) SetFramesPerSecond(value float64) error {
	value, err := boundedFPS(value)
	if err != nil {
		return err
	}
	p.mu.Lock()
	if value == p.framesPerSecond {
		p.mu.Unlock()
		return nil
	}
	p.framesPerSecond = value
	p.rateGeneration++
	p.mu.Unlock()
	p.signal()
	return nil
}

func (p *LatestFramePump) SetTargetSize(width, height int) error {
	if err := capture.ValidateTargetSize(width, height); err != nil {
		return err
	}
	target := imageSize{width, height}
	p.mu.Lock()
	if p.targetSize != nil && *p.targetSize == target {
		p.mu.Unlock()
		return nil
	}
	p.targetSize = &target
	p.targetGeneration++
	// Never deliver a frame captured for the old terminal geometry.
	p.latest = nil
	p.mu.Unlock()
	p.signal()
	return nil
}

// LatestAfter waits up to timeout seconds for a frame newer than sequence.
func (p *LatestFramePump) LatestAfter(sequence int, timeout float64) (*CapturedFrame, error) {
	deadline := time.Now().Add(time.Duration(max(0.0, timeout) * float64(time.Second)))
	for {
		p.mu.Lock()
		if p.err != nil {
			err := p.err
			p.mu.Unlock()
			return nil, fmt.Errorf("screen capture worker failed: %w", err)
		}
		if p.stop {
			p.mu.Unlock()
			return nil, nil
		}
		if p.latest != nil && p.latest.Sequence > sequence {
			p.delivered = p.latest.Sequence
			frame := p.latest
			p.mu.Unlock()
			return frame, nil
		}
		p.mu.Unlock()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, nil
		}
		timer := time.NewTimer(remaining)
		select {
		case <-p.notify:
			timer.Stop()
		case <-timer.C:
		}
	}
}

func (p *LatestFramePump) run() {
	defer close(p.done)
	lastStarted := time.Time{}
	appliedGeneration := -1
	appliedRateGeneration := -1
	for {
		p.mu.Lock()
		if p.stop {
			p.mu.Unlock()
			return
		}
		if p.targetSize == nil {
			p.err = errors.New("frame pump target size disappeared")
			p.mu.Unlock()
			p.signal()
			return
		}
		target := *p.targetSize
		generation := p.targetGeneration
		rateGeneration := p.rateGeneration
		rate := p.framesPerSecond
		p.mu.Unlock()

		targetChanged := generation != appliedGeneration
		rateChanged := rateGeneration != appliedRateGeneration
		if targetChanged {
			if err := p.capture.SetTargetSize(target.width, target.height); err != nil {
				p.fail(err)
				return
			}
			appliedGeneration = generation
		}
		if rateChanged {
			if err := p.capture.SetFrameRate(rate); err != nil {
				p.fail(err)
				return
			}
			appliedRateGeneration = rateGeneration
		}

		interval := time.Duration(float64(time.Second) / rate)
		deadline := lastStarted.Add(interval)
		if targetChanged || rateChanged {
			deadline = time.Now()
		}
		if wait := time.Until(deadline); wait > 0 {
			timer := time.NewTimer(wait)
			select {
			case <-p.notify:
				timer.Stop()
				continue
			case <-timer.C:
			}
		}

		p.mu.Lock()
		stopped := p.stop
		p.mu.Unlock()
		if stopped {
			return
		}

		lastStarted = time.Now()
		startedNs := time.Now()
		frame, err := p.capture.Capture()
		if err != nil {
			p.fail(err)
			return
		}
		captureMs := float64(time.Since(startedNs)) / 1e6

		p.mu.Lock()
		// A resize during capture invalidates the just-captured image.
		if generation != p.targetGeneration {
			p.mu.Unlock()
			continue
		}
		p.sequence++
		if p.latest != nil && p.latest.Sequence > p.delivered {
			p.DroppedFrames++
		}
		p.CapturedFrames++
		p.latest = &CapturedFrame{Frame: frame, Sequence: p.sequence, CaptureMs: captureMs}
		p.mu.Unlock()
		p.signal()
	}
}

func (p *LatestFramePump) fail(err error) {
	p.mu.Lock()
	p.err = err
	p.mu.Unlock()
	p.signal()
}

func (p *LatestFramePump) Close() {
	p.mu.Lock()
	p.stop = true
	done := p.done
	p.mu.Unlock()
	p.signal()
	if done != nil {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	}
}
