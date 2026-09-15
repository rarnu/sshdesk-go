package session

import (
	"math"

	"github.com/rarnu/sshdesk-go/internal/capture"
)

// NextAutoRenderScale computes the next automatic render scale from terminal
// backpressure signals.
func NextAutoRenderScale(current, latencyMs, writeMs float64) float64 {
	if latencyMs >= 250.0 || writeMs >= 30.0 {
		return max(0.5, round2(current*0.75))
	}
	if latencyMs >= 100.0 || writeMs >= 14.0 {
		return max(0.5, round2(current*0.85))
	}
	if current < 1.0 && latencyMs < 60.0 && writeMs > 0.0 && writeMs < 8.0 {
		return min(1.0, round2(current*1.08))
	}
	return current
}

func round2(value float64) float64 {
	return math.RoundToEven(value*100) / 100
}

// LimitForTerminalBackpressure caps the refresh rate using latency and write
// time signals.
func LimitForTerminalBackpressure(refreshRate, latencyMs, writeMs float64) float64 {
	rate := max(1.0, refreshRate)
	switch {
	case latencyMs >= 500.0:
		rate = min(rate, 10.0)
	case latencyMs >= 250.0:
		rate = min(rate, 15.0)
	case latencyMs >= 100.0:
		rate = min(rate, 30.0)
	}
	if writeMs > 0.0 {
		// SSH can accept bytes faster than the client terminal can decode
		// them, so write time is a conservative overload signal.
		rate = min(rate, 1000.0/max(1.0, writeMs*2.0))
	}
	return max(1.0, rate)
}

// CanReuseRenderedFrame reports whether the previous rendered state stays
// valid for a frame with an identical content digest.
func CanReuseRenderedFrame(hasPrevious bool, previousDigest []byte, frame *capture.Frame) bool {
	if !hasPrevious || frame == nil || frame.ContentDigest == nil {
		return false
	}
	return string(frame.ContentDigest) == string(previousDigest)
}
