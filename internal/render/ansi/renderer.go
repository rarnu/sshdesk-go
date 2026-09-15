// Package ansi renders two vertical RGB pixels per terminal cell using
// Unicode half blocks and encodes frames for truecolor, 256-color, and basic
// ANSI terminals.
package ansi

import (
	"errors"
	"image"
	"math"

	"golang.org/x/image/draw"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/render"
)

// Renderer renders a desktop frame into half-block terminal cells.
type Renderer struct {
	DeltaFullThreshold float64
	TopMargin          int
	RenderScale        float64
}

// NewRenderer validates the margin and scale bounds shared with the Python
// implementation.
func NewRenderer(deltaFullThreshold float64, topMargin int, renderScale float64) (*Renderer, error) {
	if topMargin < 0 || topMargin > 16 {
		return nil, errors.New("renderer top margin must be between 0 and 16")
	}
	if renderScale < 0.25 || renderScale > 1.0 {
		return nil, errors.New("renderer scale must be between 0.25 and 1.0")
	}
	return &Renderer{
		DeltaFullThreshold: deltaFullThreshold,
		TopMargin:          topMargin,
		RenderScale:        renderScale,
	}, nil
}

// CalculateViewport maps a desktop onto terminal cells, preserving aspect
// ratio with a centered letterbox.
func CalculateViewport(desktopWidth, desktopHeight, columns, rows, topMargin int, renderScale float64) (render.Viewport, error) {
	if columns < 1 || columns > 1024 || rows < 1 || rows > 1024 {
		return render.Viewport{}, errors.New("terminal dimensions must be between 1 and 1024")
	}
	if desktopWidth <= 0 || desktopHeight <= 0 {
		return render.Viewport{}, errors.New("desktop dimensions must be positive")
	}
	if renderScale < 0.25 || renderScale > 1.0 {
		return render.Viewport{}, errors.New("renderer scale must be between 0.25 and 1.0")
	}

	// Keep at least one content row on unusually small terminals.
	margin := min(topMargin, max(0, rows-1))
	contentRows := rows - margin
	// A terminal cell is represented by two vertical source pixels.
	scale := min(float64(columns)/float64(desktopWidth), float64(contentRows*2)/float64(desktopHeight))
	scale *= renderScale
	pixelWidth := max(1, min(columns, int(math.Round(float64(desktopWidth)*scale))))
	pixelHeight := max(2, min(contentRows*2, int(math.Round(float64(desktopHeight)*scale))))
	cellHeight := max(1, (pixelHeight+1)/2)
	x := (columns - pixelWidth) / 2
	y := margin + (contentRows-cellHeight)/2
	return render.Viewport{
		X:             x,
		Y:             y,
		Width:         pixelWidth,
		Height:        cellHeight,
		DesktopWidth:  desktopWidth,
		DesktopHeight: desktopHeight,
	}, nil
}

func (r *Renderer) viewport(desktopWidth, desktopHeight, width, height int) render.Viewport {
	viewport, err := CalculateViewport(desktopWidth, desktopHeight, width, height, r.TopMargin, r.RenderScale)
	if err != nil {
		// Unreachable: dimensions are validated before render calls.
		return render.Viewport{Width: 1, Height: 1, DesktopWidth: 1, DesktopHeight: 1}
	}
	return viewport
}

// TargetSize returns the ideal capture image size for this terminal viewport.
func (r *Renderer) TargetSize(desktopWidth, desktopHeight, width, height int) (int, int) {
	viewport := r.viewport(desktopWidth, desktopHeight, width, height)
	return viewport.Width, viewport.Height * 2
}

func (r *Renderer) Render(frame *capture.Frame, width, height int) *render.RenderedFrame {
	viewport := r.viewport(frame.Width(), frame.Height(), width, height)
	imageHeight := viewport.Height * 2
	targetWidth, targetHeight := viewport.Width, imageHeight

	var pixels *image.RGBA
	if frame.Image == nil && frame.RGB == nil {
		pixels = image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	} else {
		source := frame.RGBAImage()
		if source.Rect.Dx() == targetWidth && source.Rect.Dy() == targetHeight {
			pixels = source
		} else {
			resized := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
			draw.BiLinear.Scale(resized, resized.Rect, source, source.Rect, draw.Over, nil)
			pixels = resized
		}
	}

	black := render.Cell{}
	cells := make([]render.Cell, width*height)
	for i := range cells {
		cells[i] = black
	}
	for row := 0; row < viewport.Height; row++ {
		target := (viewport.Y+row)*width + viewport.X
		topY := row * 2
		bottomY := min(topY+1, imageHeight-1)
		for column := 0; column < viewport.Width; column++ {
			cells[target+column] = render.Cell{
				Foreground: rgbAt(pixels, column, topY),
				Background: rgbAt(pixels, column, bottomY),
			}
		}
	}
	return &render.RenderedFrame{
		TerminalWidth:  width,
		TerminalHeight: height,
		Viewport:       viewport,
		Cells:          cells,
	}
}

func rgbAt(img *image.RGBA, x, y int) render.RGB {
	i := img.PixOffset(x, y)
	return render.RGB{R: img.Pix[i], G: img.Pix[i+1], B: img.Pix[i+2]}
}

func (r *Renderer) Diff(previous, current *render.RenderedFrame) render.FrameUpdate {
	if previous == current {
		return render.FrameUpdate{Kind: render.UpdateUnchanged, Frame: current}
	}
	if previous == nil ||
		previous.TerminalWidth != current.TerminalWidth ||
		previous.TerminalHeight != current.TerminalHeight ||
		previous.Viewport != current.Viewport ||
		len(previous.Cells) != len(current.Cells) {
		return render.FrameUpdate{Kind: render.UpdateFull, Frame: current}
	}
	var changes []render.Change
	for index, cell := range current.Cells {
		if previous.Cells[index] != cell {
			changes = append(changes, render.Change{Index: index, Cell: cell})
		}
	}
	if len(changes) == 0 {
		return render.FrameUpdate{Kind: render.UpdateUnchanged, Frame: current}
	}
	if float64(len(changes))/float64(len(current.Cells)) >= r.DeltaFullThreshold {
		return render.FrameUpdate{Kind: render.UpdateFull, Frame: current}
	}
	return render.FrameUpdate{Kind: render.UpdateDelta, Frame: current, Changes: changes}
}
