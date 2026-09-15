// Package render defines the renderer interface and the shared cell/viewport
// model used by the terminal renderers.
package render

import "github.com/rarnu/sshdesk-go/internal/capture"

// Cell is one terminal cell: the foreground is the upper source pixel and the
// background is the lower source pixel.
type Cell struct {
	Foreground RGB
	Background RGB
}

type RGB struct {
	R, G, B uint8
}

// Pack returns the six-byte wire form of a cell.
func (c Cell) Pack() []byte {
	return []byte{c.Foreground.R, c.Foreground.G, c.Foreground.B, c.Background.R, c.Background.G, c.Background.B}
}

// Viewport maps a desktop onto a rectangular region of terminal cells.
type Viewport struct {
	X             int
	Y             int
	Width         int
	Height        int
	DesktopWidth  int
	DesktopHeight int
}

type RenderedFrame struct {
	TerminalWidth  int
	TerminalHeight int
	Viewport       Viewport
	Cells          []Cell
}

type UpdateKind int

const (
	UpdateUnchanged UpdateKind = iota
	UpdateFull
	UpdateDelta
)

// Change is a single changed cell index and its new value.
type Change struct {
	Index int
	Cell  Cell
}

type FrameUpdate struct {
	Kind    UpdateKind
	Frame   *RenderedFrame
	Changes []Change
}

func (u FrameUpdate) ChangedPercentage() float64 {
	total := len(u.Frame.Cells)
	if total == 0 {
		return 0
	}
	if u.Kind == UpdateFull {
		return 100.0
	}
	return float64(len(u.Changes)) * 100.0 / float64(total)
}

// StatsSnapshot is a point-in-time copy of the session metrics rendered by
// the stats overlay.
type StatsSnapshot struct {
	FPS                 float64
	CapturedFPS         float64
	CaptureMs           float64
	RenderMs            float64
	DiffMs              float64
	EncodeMs            float64
	WriteMs             float64
	FrameAgeMs          float64
	ChangedPercentage   float64
	LatencyMs           float64
	BytesSentPerSecond  float64
	BytesReceivedPerSec float64
	BytesSent           int64
	BytesReceived       int64
	TerminalWidth       int
	TerminalHeight      int
	RemoteWidth         int
	RemoteHeight        int
	FullFrames          int64
	DeltaFrames         int64
	CapturedFrames      int64
	DroppedFrames       int64
}

// Renderer converts desktop frames into terminal cell frames.
type Renderer interface {
	// TargetSize returns the ideal capture image size for this terminal viewport.
	TargetSize(desktopWidth, desktopHeight, width, height int) (int, int)
	Render(frame *capture.Frame, width, height int) *RenderedFrame
	Diff(previous, current *RenderedFrame) FrameUpdate
}
