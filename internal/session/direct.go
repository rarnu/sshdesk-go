package session

import (
	"errors"
	"fmt"
	"image"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/input"
	terminalparser "github.com/rarnu/sshdesk-go/internal/input/terminal"
	"github.com/rarnu/sshdesk-go/internal/render"
	"github.com/rarnu/sshdesk-go/internal/render/ansi"
	"github.com/rarnu/sshdesk-go/internal/render/kitty"
	"github.com/rarnu/sshdesk-go/internal/render/probe"
)

// DirectSession renders ANSI cells and consumes input through one ordinary
// SSH PTY.
type DirectSession struct {
	capture      capture.ScreenCapture
	input        input.Backend
	capabilities render.Capabilities
	deviceName   string
	sessionTitle string
	pipeline     renderPipeline
	pixelMouse   bool
	parser       *terminalparser.Parser
	stats        *Stats

	stopOnce    sync.Once
	stopCh      chan struct{}
	wakeCh      chan struct{}
	interrupted atomic.Bool

	showStats atomic.Bool

	currentMu sync.Mutex
	current   any

	cursorMu      sync.Mutex
	pendingCursor *image.Point

	controls chan any

	lastActivity     atomic.Int64 // nanoseconds, monotonic clock
	latencyProbeNs   atomic.Int64 // nanoseconds; 0 means no pending probe
	lastLatencyProbe float64

	requestedMaxFPS      *float64
	requestedRenderScale *float64
	autoRenderScale      bool
	renderScale          float64
	lastScaleAdjust      float64
	activeFPS            float64
	pump                 *LatestFramePump

	inputDone chan struct{}
}

// NewDirectSession builds a session over the given backends. maxFPS and
// renderScale may be nil to follow SSHDESK_MAX_FPS / SSHDESK_SCALE.
func NewDirectSession(
	captureBackend capture.ScreenCapture,
	inputBackend input.Backend,
	capabilities render.Capabilities,
	maxFPS *float64,
	renderScale *float64,
) *DirectSession {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "remote"
	}
	session := &DirectSession{
		capture:      captureBackend,
		input:        inputBackend,
		capabilities: capabilities,
		deviceName:   hostname,
		parser:       &terminalparser.Parser{},
		stats:        NewStats(),
		stopCh:       make(chan struct{}),
		wakeCh:       make(chan struct{}, 1),
		controls:     make(chan any, 64),
		inputDone:    make(chan struct{}),
		renderScale:  1.0,
		activeFPS:    30.0,
	}
	session.sessionTitle = "SSHDESK - " + hostname
	session.requestedMaxFPS = maxFPS
	session.requestedRenderScale = renderScale
	session.lastActivity.Store(time.Now().UnixNano())
	return session
}

// Stats exposes the session metrics.
func (s *DirectSession) Stats() *Stats { return s.stats }

func (s *DirectSession) requestStop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	s.wake()
}

func (s *DirectSession) stopped() bool {
	select {
	case <-s.stopCh:
		return true
	default:
		return false
	}
}

func (s *DirectSession) wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// waitWake blocks until woken or timeout; true means a wake was consumed.
func (s *DirectSession) waitWake(timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-s.wakeCh:
		return true
	case <-timer.C:
		return false
	}
}

func (s *DirectSession) getCurrent() any {
	s.currentMu.Lock()
	defer s.currentMu.Unlock()
	return s.current
}

func (s *DirectSession) setCurrent(frame any) {
	s.currentMu.Lock()
	s.current = frame
	s.currentMu.Unlock()
}

func (s *DirectSession) takePendingCursor() *image.Point {
	s.cursorMu.Lock()
	defer s.cursorMu.Unlock()
	cursor := s.pendingCursor
	s.pendingCursor = nil
	return cursor
}

func monotonicNow() float64 {
	return float64(time.Now().UnixNano()) / 1e9
}

func (s *DirectSession) configureRenderer(inputFd, outputFd int) error {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("SSHDESK_RENDER")))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto", "ansi", "kitty":
	default:
		return fmt.Errorf("SSHDESK_RENDER must be auto, ansi, or kitty")
	}

	scaleValue := os.Getenv("SSHDESK_SCALE")
	auto := render.IsAutoRenderScale(scaleValue)
	if s.requestedRenderScale != nil {
		auto = false
	}
	var scale float64
	if s.requestedRenderScale != nil {
		parsed, err := render.ParseRenderScale(formatFloat(*s.requestedRenderScale))
		if err != nil {
			return err
		}
		scale = parsed
	} else {
		parsed, err := render.ParseRenderScale(scaleValue)
		if err != nil {
			return err
		}
		scale = parsed
	}
	s.autoRenderScale = auto
	s.renderScale = scale
	renderer, err := ansi.NewRenderer(0.60, 1, scale)
	if err != nil {
		return err
	}
	s.pipeline = &ansiPipeline{
		renderer: renderer,
		writer:   ansi.NewWriter(s.capabilities, s.sessionTitle),
	}
	if mode == "ansi" {
		return nil
	}
	columns, rows := Size(outputFd)
	graphics := probe.ProbeGraphics(inputFd, outputFd, columns, rows, probe.ProbeTimeout)
	if graphics.Usable() {
		kittyRenderer, err := kitty.NewRenderer(graphics, 1, scale)
		if err != nil {
			return err
		}
		s.pipeline = &kittyPipeline{
			renderer: kittyRenderer,
			writer:   kitty.NewWriter(s.capabilities, graphics, s.sessionTitle),
		}
		s.pixelMouse = graphics.PixelMouse
		return nil
	}
	if mode == "kitty" {
		if graphics.KittyGraphics {
			return errors.New("terminal supports Kitty graphics but reported no pixel geometry")
		}
		return errors.New("terminal does not support Kitty graphics; use kitty, Ghostty, or WezTerm")
	}
	return nil
}

func formatFloat(value float64) string {
	return fmt.Sprintf("%v", value)
}

func (s *DirectSession) resolveActiveFPS() error {
	_, sharp := s.pipeline.(*kittyPipeline)
	value := os.Getenv("SSHDESK_MAX_FPS")
	if s.requestedMaxFPS != nil {
		parsed, err := render.ParseMaxFPS(formatFloat(*s.requestedMaxFPS), sharp)
		if err != nil {
			return err
		}
		s.activeFPS = parsed
		return nil
	}
	parsed, err := render.ParseMaxFPS(value, sharp)
	if err != nil {
		return err
	}
	s.activeFPS = parsed
	return nil
}

func (s *DirectSession) estimatedLatencyMs(snapshotLatencyMs float64) float64 {
	latencyMs := snapshotLatencyMs
	if ns := s.latencyProbeNs.Load(); ns != 0 {
		latencyMs = max(latencyMs, max(0.0, float64(time.Now().UnixNano()-ns)/1e6))
	}
	return latencyMs
}

func (s *DirectSession) maybeAdjustRenderScale(now float64) bool {
	if !s.autoRenderScale {
		return false
	}
	snapshot := s.stats.Snapshot()
	next := NextAutoRenderScale(s.renderScale, s.estimatedLatencyMs(snapshot.LatencyMs), snapshot.WriteMs)
	if next == s.renderScale {
		return false
	}
	cooldown := 8.0
	if next < s.renderScale {
		cooldown = 2.0
	}
	if now-s.lastScaleAdjust < cooldown {
		return false
	}
	s.lastScaleAdjust = now
	s.renderScale = next
	s.pipeline.setRenderScale(next)
	return true
}

// refreshRate computes the presentation rate from idle time and backpressure.
func (s *DirectSession) refreshRate(now, activity float64) float64 {
	idle := now - activity
	var rate float64
	switch {
	case idle < 1.0:
		rate = s.activeFPS
	case idle < 5.0:
		rate = min(s.activeFPS, 30.0)
	default:
		rate = min(s.activeFPS, 2.0)
	}
	snapshot := s.stats.Snapshot()
	return LimitForTerminalBackpressure(rate, s.estimatedLatencyMs(snapshot.LatencyMs), snapshot.WriteMs)
}

func (s *DirectSession) handleEvent(event input.Event) bool {
	switch ev := event.(type) {
	case input.ControlEvent:
		switch ev.Kind {
		case input.ControlExit:
			s.requestStop()
			return true
		case input.ControlToggleStats:
			s.controls <- ev.Kind
			return true
		}
		return false
	case input.TerminalReportEvent:
		if ns := s.latencyProbeNs.Swap(0); ns != 0 {
			s.stats.SetLatency(max(0.0, float64(time.Now().UnixNano()-ns)/1e6))
		}
		return false
	case input.KeyEvent:
		s.input.Key(ev)
		return true
	}
	var column, row int
	switch ev := event.(type) {
	case input.MouseMoveEvent:
		column, row = ev.Column, ev.Row
	case input.MouseButtonEvent:
		column, row = ev.Column, ev.Row
	case input.MouseScrollEvent:
		column, row = ev.Column, ev.Row
	default:
		return false
	}
	current := s.getCurrent()
	if current == nil {
		return false
	}
	var x, y int
	var ok bool
	if s.pixelMouse {
		if kittyFrame, isKitty := current.(*kitty.RenderedFrame); isKitty {
			x, y, ok = kitty.TranslatePixelCoordinates(column, row, kittyFrame.PixelViewport)
		}
	} else if ansiFrame, isANSI := current.(*render.RenderedFrame); isANSI {
		x, y, ok = terminalparser.TranslateCoordinates(column, row, ansiFrame.Viewport)
	}
	if !ok {
		return false
	}
	switch ev := event.(type) {
	case input.MouseMoveEvent:
		s.input.Move(x, y)
	case input.MouseButtonEvent:
		s.input.Button(ev.Button, ev.Pressed, x, y)
	case input.MouseScrollEvent:
		s.input.Scroll(ev.Amount, x, y)
	}
	// Cursor placement is independent of the captured framebuffer. Keep only
	// the newest coordinate so rapid terminal motion cannot build a queue.
	s.cursorMu.Lock()
	point := image.Point{X: x, Y: y}
	s.pendingCursor = &point
	s.cursorMu.Unlock()
	return true
}

// inputLoop reads and injects input independently so slow frame writes
// cannot starve it.
func (s *DirectSession) inputLoop(inputFd int) {
	defer close(s.inputDone)
	buffer := make([]byte, 4096)
	for !s.stopped() {
		n, timedOut, err := readTerminalInput(inputFd, buffer, 50*time.Millisecond)
		if err != nil {
			s.controls <- err
			s.requestStop()
			return
		}
		var events []input.Event
		if timedOut {
			events, err = s.parser.FlushNow()
		} else {
			if n == 0 {
				s.requestStop()
				return
			}
			s.stats.AddReceived(n)
			events, err = s.parser.FeedNow(buffer[:n])
		}
		if err != nil {
			s.controls <- err
			s.requestStop()
			return
		}
		for _, event := range terminalparser.CoalesceMouseMoves(events) {
			if s.handleEvent(event) {
				s.lastActivity.Store(time.Now().UnixNano())
				s.wake()
			}
		}
	}
}

func (s *DirectSession) drawExtras(outputFd int, cursor *image.Point, drawStats, drawHeader bool) int {
	var output []byte
	if drawHeader {
		width := 80
		if current := s.getCurrent(); current != nil {
			width = frameTerminalWidth(current)
		}
		output = append(output, s.pipeline.header(width)...)
	}
	if drawStats && s.showStats.Load() {
		output = append(output, s.pipeline.overlay(s.stats.Snapshot())...)
	}
	current := s.getCurrent()
	if cursor != nil && current != nil {
		output = append(output, s.pipeline.cursor(current, cursor.X, cursor.Y, true)...)
	}
	if len(output) > 0 {
		_ = WriteAll(outputFd, output)
		s.stats.AddSent(len(output))
	}
	return len(output)
}

func (s *DirectSession) resetCanvas(outputFd int) {
	reset := s.pipeline.resetCanvas()
	_ = WriteAll(outputFd, reset)
	s.stats.AddSent(len(reset))
}

// Run drives the render loop until stop and returns the process exit code.
func (s *DirectSession) Run() int {
	code, err := s.run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-server: %v\n", err)
	}
	return code
}

func (s *DirectSession) run() (int, error) {
	var previous any
	lastChange := monotonicNow()
	lastStatsDraw := 0.0
	var lastCursor *image.Point
	frameSequence := 0
	nextFrame := 0.0
	var lastContentDigest []byte

	restoreSignals := s.watchSignals()
	defer restoreSignals()
	defer func() {
		s.requestStop()
		if s.pump != nil {
			s.pump.Close()
			s.pump = nil
		}
		select {
		case <-s.inputDone:
		case <-time.After(time.Second):
		}
		s.pipeline.close()
		s.input.Close()
		s.capture.Close()
	}()

	if err := s.configureRenderer(int(os.Stdin.Fd()), int(os.Stdout.Fd())); err != nil {
		return 1, err
	}
	if err := s.resolveActiveFPS(); err != nil {
		return 1, err
	}

	terminal := NewTerminalState(-1, -1, pipelineWriterView{s.pipeline})
	if err := terminal.Enter(); err != nil {
		return 1, err
	}
	defer terminal.Restore()

	dimensions := image.Point{}
	dimensions.X, dimensions.Y = Size(terminal.OutputFd)
	desktopW, desktopH := s.capture.Size()

	pump, err := NewLatestFramePump(s.capture, s.activeFPS)
	if err != nil {
		return 1, err
	}
	s.pump = pump
	targetW, targetH := s.pipeline.targetSize(desktopW, desktopH, dimensions.X, dimensions.Y)
	if err := pump.SetTargetSize(targetW, targetH); err != nil {
		return 1, err
	}
	if err := pump.Start(); err != nil {
		return 1, err
	}

	go s.inputLoop(terminal.InputFd)

	for !s.stopped() {
		// Drain control events.
		for draining := true; draining; {
			select {
			case control := <-s.controls:
				switch value := control.(type) {
				case error:
					return 1, value
				case input.ControlKind:
					if value == input.ControlToggleStats {
						s.toggleStats(terminal.OutputFd, lastCursor)
					}
				}
			default:
				draining = false
			}
		}

		// Do not wait for a desktop frame before moving the cursor; the
		// terminal-side cursor update is tiny and keeps motion responsive.
		if pending := s.takePendingCursor(); pending != nil && !pointsEqual(pending, lastCursor) {
			s.drawExtras(terminal.OutputFd, pending, false, false)
			lastCursor = pending
		}

		newWidth, newHeight := Size(terminal.OutputFd)
		if newWidth != dimensions.X || newHeight != dimensions.Y {
			dimensions = image.Point{X: newWidth, Y: newHeight}
			targetW, targetH := s.pipeline.targetSize(desktopW, desktopH, dimensions.X, dimensions.Y)
			if err := pump.SetTargetSize(targetW, targetH); err != nil {
				return 1, err
			}
			previous = nil
			lastContentDigest = nil
			s.setCurrent(nil)
			s.resetCanvas(terminal.OutputFd)
			nextFrame = 0.0
		}

		now := monotonicNow()
		if s.maybeAdjustRenderScale(now) {
			targetW, targetH := s.pipeline.targetSize(desktopW, desktopH, dimensions.X, dimensions.Y)
			if err := pump.SetTargetSize(targetW, targetH); err != nil {
				return 1, err
			}
			previous = nil
			lastContentDigest = nil
			s.setCurrent(nil)
			s.resetCanvas(terminal.OutputFd)
			nextFrame = 0.0
			continue
		}

		activity := max(float64(s.lastActivity.Load())/1e9, lastChange)
		refresh := s.refreshRate(now, activity)
		if now < nextFrame {
			if s.waitWake(minDuration(secondsDuration(nextFrame-now), 50*time.Millisecond)) {
				nextFrame = 0.0
			}
			continue
		}
		captured, err := pump.LatestAfter(frameSequence, 0.05)
		if err != nil {
			return 1, err
		}
		if captured == nil {
			continue
		}
		frameSequence = captured.Sequence
		frame := captured.Frame
		now = monotonicNow()
		// Capture keeps running at the active rate; only presentation slows
		// while idle, avoiding a stale 2 FPS stream on input.
		nextFrame = now + 1.0/refresh
		started := time.Now()

		if frame.Width() != desktopW || frame.Height() != desktopH {
			desktopW, desktopH = frame.Width(), frame.Height()
			targetW, targetH := s.pipeline.targetSize(desktopW, desktopH, dimensions.X, dimensions.Y)
			if err := pump.SetTargetSize(targetW, targetH); err != nil {
				return 1, err
			}
			previous = nil
			lastContentDigest = nil
		}
		var rendered any
		if CanReuseRenderedFrame(previous != nil, lastContentDigest, frame) {
			rendered = previous
		} else {
			rendered = s.pipeline.renderFrame(frame, dimensions.X, dimensions.Y)
		}
		lastContentDigest = frame.ContentDigest
		renderedAt := time.Now()
		update := s.pipeline.diffFrames(previous, rendered)
		diffed := time.Now()
		previous = rendered
		s.setCurrent(rendered)
		s.stats.Dimensions(dimensions.X, dimensions.Y, frame.Width(), frame.Height())
		s.stats.CapturePipeline(pump.CapturedFrames, pump.DroppedFrames)

		cursor := s.takePendingCursor()
		if cursor == nil {
			if x, y, ok := s.capture.CursorPosition(); ok {
				cursor = &image.Point{X: x, Y: y}
			}
		}
		cursorChanged := !pointsEqual(cursor, lastCursor)
		drawStats := s.showStats.Load() && now-lastStatsDraw >= 1.0

		if kind := updateKind(update); kind != render.UpdateUnchanged {
			encoded := s.pipeline.update(update)
			encodedAt := time.Now()
			if err := WriteAll(terminal.OutputFd, encoded); err != nil {
				return 1, err
			}
			writtenAt := time.Now()
			s.stats.AddSent(len(encoded))
			lastChange = now
			frameAgeMs := 0.0
			if frame.CapturedNs > 0 {
				frameAgeMs = max(0.0, float64(time.Now().UnixNano()-frame.CapturedNs)/1e6)
			}
			s.stats.RecordFrame(
				captured.CaptureMs,
				float64(renderedAt.Sub(started))/1e6,
				float64(diffed.Sub(renderedAt))/1e6,
				float64(encodedAt.Sub(diffed))/1e6,
				float64(writtenAt.Sub(encodedAt))/1e6,
				frameAgeMs,
				updateChangedPercentage(update),
				kind == render.UpdateFull,
			)
			// Frame updates can overwrite the overlay and move the terminal
			// cursor.
			s.drawExtras(terminal.OutputFd, cursor, s.showStats.Load(), kind == render.UpdateFull)
			lastStatsDraw = now
		} else if cursorChanged || drawStats {
			s.drawExtras(terminal.OutputFd, cursor, drawStats, false)
			if drawStats {
				lastStatsDraw = now
			}
		}
		lastCursor = cursor

		probeNs := s.latencyProbeNs.Load()
		if now-s.lastLatencyProbe >= 1.0 &&
			(probeNs == 0 || time.Now().UnixNano()-probeNs > 5_000_000_000) {
			s.latencyProbeNs.Store(time.Now().UnixNano())
			probe := s.pipeline.latencyProbe()
			if err := WriteAll(terminal.OutputFd, probe); err != nil {
				return 1, err
			}
			s.stats.AddSent(len(probe))
			s.lastLatencyProbe = now
		}
	}
	if s.interrupted.Load() {
		return 130, nil
	}
	return 0, nil
}

func (s *DirectSession) toggleStats(outputFd int, lastCursor *image.Point) {
	show := !s.showStats.Load()
	s.showStats.Store(show)
	if !show {
		if current := s.getCurrent(); current != nil {
			encoded := s.pipeline.full(current)
			_ = WriteAll(outputFd, encoded)
			s.stats.AddSent(len(encoded))
		}
	}
	s.drawExtras(outputFd, lastCursor, show, true)
}

func pointsEqual(a, b *image.Point) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func secondsDuration(value float64) time.Duration {
	if value <= 0 {
		return 0
	}
	return time.Duration(value * float64(time.Second))
}
