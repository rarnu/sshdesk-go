package session

import (
	"sync"
	"time"

	"github.com/rarnu/sshdesk-go/internal/render"
)

type ring struct {
	values []float64
	next   int
	count  int
}

func newRing(capacity int) *ring {
	return &ring{values: make([]float64, capacity)}
}

func (r *ring) add(value float64) {
	r.values[r.next] = value
	r.next = (r.next + 1) % len(r.values)
	if r.count < len(r.values) {
		r.count++
	}
}

func (r *ring) average() float64 {
	if r.count == 0 {
		return 0
	}
	sum := 0.0
	for i := 0; i < r.count; i++ {
		sum += r.values[i]
	}
	return sum / float64(r.count)
}

func monotonicSeconds() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

// Stats is low-overhead rolling performance instrumentation shared between
// the session goroutines.
type Stats struct {
	mu sync.Mutex

	frameTimes    []float64
	captureMs     *ring
	renderMs      *ring
	diffMs        *ring
	encodeMs      *ring
	writeMs       *ring
	frameAgeMs    *ring
	changed       *ring
	bytesSent     int64
	bytesReceived int64
	terminalWidth int
	terminalHgt   int
	remoteWidth   int
	remoteHeight  int
	fullFrames    int64
	deltaFrames   int64
	captured      int64
	dropped       int64
	latencyMs     float64

	lastRateTime     float64
	lastRateSent     int64
	lastRateReceived int64
	lastRateCaptured int64
	sentRate         float64
	receivedRate     float64
	captureRate      float64
}

func NewStats() *Stats {
	return &Stats{
		captureMs:    newRing(60),
		renderMs:     newRing(60),
		diffMs:       newRing(60),
		encodeMs:     newRing(60),
		writeMs:      newRing(60),
		frameAgeMs:   newRing(60),
		changed:      newRing(60),
		lastRateTime: monotonicSeconds(),
	}
}

// AddSent accumulates bytes written to the terminal.
func (s *Stats) AddSent(n int) {
	s.mu.Lock()
	s.bytesSent += int64(n)
	s.mu.Unlock()
}

// AddReceived accumulates bytes read from the terminal.
func (s *Stats) AddReceived(n int) {
	s.mu.Lock()
	s.bytesReceived += int64(n)
	s.mu.Unlock()
}

// SetLatency stores the latest measured RTT in milliseconds.
func (s *Stats) SetLatency(ms float64) {
	s.mu.Lock()
	s.latencyMs = ms
	s.mu.Unlock()
}

// RecordFrame records one presented frame's stage timings.
func (s *Stats) RecordFrame(captureMs, renderMs, diffMs, encodeMs, writeMs, frameAgeMs, changedPercentage float64, full bool) {
	now := monotonicSeconds()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.frameTimes = append(s.frameTimes, now)
	for len(s.frameTimes) > 0 && s.frameTimes[0] < now-1.0 {
		s.frameTimes = s.frameTimes[1:]
	}
	s.captureMs.add(captureMs)
	s.renderMs.add(renderMs)
	s.diffMs.add(diffMs)
	s.encodeMs.add(encodeMs)
	s.writeMs.add(writeMs)
	s.frameAgeMs.add(frameAgeMs)
	s.changed.add(changedPercentage)
	if full {
		s.fullFrames++
	} else {
		s.deltaFrames++
	}
}

// Dimensions records the terminal and remote sizes.
func (s *Stats) Dimensions(terminalWidth, terminalHeight, remoteWidth, remoteHeight int) {
	s.mu.Lock()
	s.terminalWidth, s.terminalHgt = terminalWidth, terminalHeight
	s.remoteWidth, s.remoteHeight = remoteWidth, remoteHeight
	s.mu.Unlock()
}

// CapturePipeline records cumulative capture/drop counters.
func (s *Stats) CapturePipeline(captured, dropped int) {
	s.mu.Lock()
	s.captured = int64(max(0, captured))
	s.dropped = int64(max(0, dropped))
	s.mu.Unlock()
}

// Snapshot returns a consistent point-in-time copy of the metrics.
func (s *Stats) Snapshot() render.StatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := monotonicSeconds()
	for len(s.frameTimes) > 0 && s.frameTimes[0] < now-1.0 {
		s.frameTimes = s.frameTimes[1:]
	}
	elapsed := now - s.lastRateTime
	if elapsed >= 0.25 {
		s.sentRate = float64(s.bytesSent-s.lastRateSent) / elapsed
		s.receivedRate = float64(s.bytesReceived-s.lastRateReceived) / elapsed
		s.captureRate = float64(s.captured-s.lastRateCaptured) / elapsed
		s.lastRateSent = s.bytesSent
		s.lastRateReceived = s.bytesReceived
		s.lastRateCaptured = s.captured
		s.lastRateTime = now
	}
	return render.StatsSnapshot{
		FPS:                 float64(len(s.frameTimes)),
		CapturedFPS:         s.captureRate,
		CaptureMs:           s.captureMs.average(),
		RenderMs:            s.renderMs.average(),
		DiffMs:              s.diffMs.average(),
		EncodeMs:            s.encodeMs.average(),
		WriteMs:             s.writeMs.average(),
		FrameAgeMs:          s.frameAgeMs.average(),
		ChangedPercentage:   s.changed.average(),
		LatencyMs:           s.latencyMs,
		BytesSentPerSecond:  s.sentRate,
		BytesReceivedPerSec: s.receivedRate,
		BytesSent:           s.bytesSent,
		BytesReceived:       s.bytesReceived,
		TerminalWidth:       s.terminalWidth,
		TerminalHeight:      s.terminalHgt,
		RemoteWidth:         s.remoteWidth,
		RemoteHeight:        s.remoteHeight,
		FullFrames:          s.fullFrames,
		DeltaFrames:         s.deltaFrames,
		CapturedFrames:      s.captured,
		DroppedFrames:       s.dropped,
	}
}
