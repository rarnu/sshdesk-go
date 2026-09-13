package session

import (
	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/render"
	"github.com/rylena/sshdesk-go/internal/render/ansi"
	"github.com/rylena/sshdesk-go/internal/render/kitty"
)

// renderPipeline abstracts the ANSI cell renderer and the Kitty pixel
// renderer behind one frame/writer contract so the session loop stays
// renderer-agnostic, mirroring the Python duck-typed renderer/writer pair.
type renderPipeline interface {
	// Renderer side.
	targetSize(desktopWidth, desktopHeight, width, height int) (int, int)
	renderFrame(frame *capture.Frame, width, height int) any
	diffFrames(previous, current any) any
	setRenderScale(scale float64)

	// Writer side.
	enter() []byte
	leave() []byte
	close()
	header(width int) []byte
	overlay(stats render.StatsSnapshot) []byte
	update(update any) []byte
	full(frame any) []byte
	cursor(frame any, remoteX, remoteY int, visible bool) []byte
	resetCanvas() []byte
	latencyProbe() []byte
}

func updateKind(update any) render.UpdateKind {
	switch value := update.(type) {
	case render.FrameUpdate:
		return value.Kind
	case kitty.FrameUpdate:
		return value.Kind
	default:
		return render.UpdateUnchanged
	}
}

func updateChangedPercentage(update any) float64 {
	switch value := update.(type) {
	case render.FrameUpdate:
		return value.ChangedPercentage()
	case kitty.FrameUpdate:
		return value.ChangedPercentage()
	default:
		return 0
	}
}

func frameTerminalWidth(frame any) int {
	switch value := frame.(type) {
	case *render.RenderedFrame:
		return value.TerminalWidth
	case *kitty.RenderedFrame:
		return value.TerminalWidth
	default:
		return 0
	}
}

// ansiPipeline adapts the half-block ANSI renderer and writer.
type ansiPipeline struct {
	renderer *ansi.Renderer
	writer   *ansi.Writer
}

func (p *ansiPipeline) targetSize(desktopWidth, desktopHeight, width, height int) (int, int) {
	return p.renderer.TargetSize(desktopWidth, desktopHeight, width, height)
}

func (p *ansiPipeline) renderFrame(frame *capture.Frame, width, height int) any {
	return p.renderer.Render(frame, width, height)
}

func (p *ansiPipeline) diffFrames(previous, current any) any {
	var before *render.RenderedFrame
	if previous != nil {
		before = previous.(*render.RenderedFrame)
	}
	return p.renderer.Diff(before, current.(*render.RenderedFrame))
}

func (p *ansiPipeline) setRenderScale(scale float64) { p.renderer.RenderScale = scale }

func (p *ansiPipeline) enter() []byte { return p.writer.Enter() }
func (p *ansiPipeline) leave() []byte { return p.writer.Leave() }
func (p *ansiPipeline) close()        { p.writer.Close() }
func (p *ansiPipeline) header(w int) []byte {
	return p.writer.Header(w)
}
func (p *ansiPipeline) overlay(stats render.StatsSnapshot) []byte {
	return p.writer.Overlay(stats)
}
func (p *ansiPipeline) update(update any) []byte {
	return p.writer.Update(update.(render.FrameUpdate))
}
func (p *ansiPipeline) full(frame any) []byte {
	return p.writer.Full(frame.(*render.RenderedFrame))
}
func (p *ansiPipeline) cursor(frame any, remoteX, remoteY int, visible bool) []byte {
	return p.writer.Cursor(frame.(*render.RenderedFrame), remoteX, remoteY, visible)
}
func (p *ansiPipeline) resetCanvas() []byte  { return []byte("\x1b[2J\x1b[H") }
func (p *ansiPipeline) latencyProbe() []byte { return ansi.LatencyProbe() }

// kittyPipeline adapts the Kitty pixel renderer and writer.
type kittyPipeline struct {
	renderer *kitty.Renderer
	writer   *kitty.Writer
}

func (p *kittyPipeline) targetSize(desktopWidth, desktopHeight, width, height int) (int, int) {
	return p.renderer.TargetSize(desktopWidth, desktopHeight, width, height)
}

func (p *kittyPipeline) renderFrame(frame *capture.Frame, width, height int) any {
	return p.renderer.Render(frame, width, height)
}

func (p *kittyPipeline) diffFrames(previous, current any) any {
	var before *kitty.RenderedFrame
	if previous != nil {
		before = previous.(*kitty.RenderedFrame)
	}
	return p.renderer.Diff(before, current.(*kitty.RenderedFrame))
}

func (p *kittyPipeline) setRenderScale(scale float64) { p.renderer.RenderScale = scale }

func (p *kittyPipeline) enter() []byte { return p.writer.Enter() }
func (p *kittyPipeline) leave() []byte { return p.writer.Leave() }
func (p *kittyPipeline) close()        { p.writer.Close() }
func (p *kittyPipeline) header(w int) []byte {
	return p.writer.Header(w)
}
func (p *kittyPipeline) overlay(stats render.StatsSnapshot) []byte {
	return p.writer.Overlay(stats)
}
func (p *kittyPipeline) update(update any) []byte {
	return p.writer.Update(update.(kitty.FrameUpdate))
}
func (p *kittyPipeline) full(frame any) []byte {
	return p.writer.Full(frame.(*kitty.RenderedFrame))
}
func (p *kittyPipeline) cursor(frame any, remoteX, remoteY int, visible bool) []byte {
	return p.writer.Cursor(frame.(*kitty.RenderedFrame), remoteX, remoteY, visible)
}
func (p *kittyPipeline) resetCanvas() []byte  { return p.writer.ResetCanvas() }
func (p *kittyPipeline) latencyProbe() []byte { return ansi.LatencyProbe() }

// pipelineWriterView exposes the Enter/Leave pair TerminalState needs.
type pipelineWriterView struct {
	pipeline renderPipeline
}

func (v pipelineWriterView) Enter() []byte { return v.pipeline.enter() }
func (v pipelineWriterView) Leave() []byte { return v.pipeline.leave() }
