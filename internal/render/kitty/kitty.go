// Package kitty renders the desktop as changed RGB tiles in terminal pixel
// space using the Kitty graphics protocol.
package kitty

import (
	"errors"
	"image"
	"math"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/capture/xshm"
	"github.com/rarnu/sshdesk-go/internal/render"
	"github.com/rarnu/sshdesk-go/internal/render/probe"
)

const (
	ChunkSize            = 4096
	fullImageID0         = 0x7500
	fullImageID1         = 0x7501
	ImageIDBase          = 0x7600
	CursorImageID        = 0x47600
	PlacementID          = 1
	TileTargetPixels     = 160
	FullReplaceThreshold = 15.0
)

// PixelViewport maps a desktop onto a rectangular region of terminal pixels.
type PixelViewport struct {
	X             int
	Y             int
	Width         int
	Height        int
	DesktopWidth  int
	DesktopHeight int
}

// Tile is one placed image rectangle in terminal cell coordinates.
type Tile struct {
	ImageID     int
	Column      int
	Row         int
	Width       int
	Height      int
	CellColumns int
	CellRows    int
	Digest      []byte
	RGB         []byte
}

// RenderedFrame is a desktop frame laid out in terminal pixel space. The
// content pixels are either Image (RGBA) or packed RGB24 in RGB with
// RGBWidth/RGBHeight, mirroring the form the capture backend delivered.
type RenderedFrame struct {
	TerminalWidth  int
	TerminalHeight int
	Viewport       render.Viewport
	PixelViewport  PixelViewport
	CellWidth      int
	CellHeight     int
	Image          *image.RGBA
	RGB            []byte
	RGBWidth       int
	RGBHeight      int
	Tiles          []Tile
}

// contentSize returns the content pixel dimensions in either encoding.
func (f *RenderedFrame) contentSize() (int, int) {
	if f.Image != nil {
		return f.Image.Rect.Dx(), f.Image.Rect.Dy()
	}
	return f.RGBWidth, f.RGBHeight
}

// FrameUpdate is a rendered frame plus the tiles that changed.
type FrameUpdate struct {
	Kind    render.UpdateKind
	Frame   *RenderedFrame
	Changes []Tile
}

// ChangedPercentage is the share of content pixels covered by the update.
func (u FrameUpdate) ChangedPercentage() float64 {
	contentWidth, contentHeight := u.Frame.contentSize()
	total := contentWidth * contentHeight
	if total == 0 {
		return 0.0
	}
	if u.Kind == render.UpdateFull {
		return 100.0
	}
	changed := 0
	for _, tile := range u.Changes {
		changed += tile.Width * tile.Height
	}
	return min(100.0, float64(changed)*100.0/float64(total))
}

// TranslatePixelCoordinates maps terminal pixel coordinates (SGR ?1016
// reporting) into desktop coordinates.
func TranslatePixelCoordinates(x, y int, viewport PixelViewport) (int, int, bool) {
	if !(viewport.X <= x && x < viewport.X+viewport.Width &&
		viewport.Y <= y && y < viewport.Y+viewport.Height) {
		return 0, 0, false
	}
	localX := x - viewport.X
	localY := y - viewport.Y
	remoteX := min(viewport.DesktopWidth-1,
		int((float64(localX)+0.5)*float64(viewport.DesktopWidth)/float64(viewport.Width)))
	remoteY := min(viewport.DesktopHeight-1,
		int((float64(localY)+0.5)*float64(viewport.DesktopHeight)/float64(viewport.Height)))
	return remoteX, remoteY, true
}

// Renderer renders a desktop frame into Kitty graphics tiles.
type Renderer struct {
	Probe       probe.GraphicsProbe
	TopMargin   int
	RenderScale float64
}

// NewRenderer validates the probe, margin, and scale bounds shared with the
// Python implementation.
func NewRenderer(p probe.GraphicsProbe, topMargin int, renderScale float64) (*Renderer, error) {
	if !p.Usable() {
		return nil, errors.New("Kitty rendering requires graphics and terminal pixel geometry")
	}
	if topMargin < 0 || topMargin > 16 {
		return nil, errors.New("renderer top margin must be between 0 and 16")
	}
	if renderScale < 0.25 || renderScale > 1.0 {
		return nil, errors.New("renderer scale must be between 0.25 and 1.0")
	}
	return &Renderer{Probe: p, TopMargin: topMargin, RenderScale: renderScale}, nil
}

// pyRound mirrors Python's round(): banker's rounding to the nearest integer.
func pyRound(value float64) int {
	return int(math.RoundToEven(value))
}

func (r *Renderer) layout(
	desktopWidth, desktopHeight, width, height int,
) (render.Viewport, PixelViewport, int, int, int, int, error) {
	if width < 1 || width > 1024 || height < 1 || height > 1024 {
		return render.Viewport{}, PixelViewport{}, 0, 0, 0, 0,
			errors.New("terminal dimensions must be between 1 and 1024")
	}
	if desktopWidth <= 0 || desktopHeight <= 0 {
		return render.Viewport{}, PixelViewport{}, 0, 0, 0, 0,
			errors.New("desktop dimensions must be positive")
	}
	cellWidth := r.Probe.CellWidth
	cellHeight := r.Probe.CellHeight
	margin := min(r.TopMargin, max(0, height-1))
	contentRows := height - margin
	usableWidth := width * cellWidth
	usableHeight := contentRows * cellHeight
	// The layout always fills the terminal as far as the desktop aspect ratio
	// allows: images are placed with Kitty's c/r cell-span keys, so the
	// TERMINAL scales each image to the placed cell area (including upscaling
	// a small desktop). Only the transmitted content pixels are still capped
	// at the desktop's native resolution times render_scale.
	displayScale := min(
		float64(usableWidth)/float64(desktopWidth),
		float64(usableHeight)/float64(desktopHeight),
	)
	idealWidth := max(float64(cellWidth), float64(desktopWidth)*displayScale)
	idealHeight := max(float64(cellHeight), float64(desktopHeight)*displayScale)
	cellColumns := max(1, min(width, pyRound(idealWidth/float64(cellWidth))))
	cellRows := max(1, min(contentRows, pyRound(idealHeight/float64(cellHeight))))
	contentScale := min(1.0, displayScale) * r.RenderScale
	imageWidth := max(1, pyRound(float64(desktopWidth)*contentScale))
	imageHeight := max(1, pyRound(float64(desktopHeight)*contentScale))
	left := (width - cellColumns) / 2
	top := margin + (contentRows-cellRows)/2
	viewport := render.Viewport{
		X:             left,
		Y:             top,
		Width:         cellColumns,
		Height:        cellRows,
		DesktopWidth:  desktopWidth,
		DesktopHeight: desktopHeight,
	}
	pixelViewport := PixelViewport{
		X:             left * cellWidth,
		Y:             top * cellHeight,
		Width:         cellColumns * cellWidth,
		Height:        cellRows * cellHeight,
		DesktopWidth:  desktopWidth,
		DesktopHeight: desktopHeight,
	}
	return viewport, pixelViewport, cellWidth, cellHeight, imageWidth, imageHeight, nil
}

// TargetSize returns the ideal capture image size for this terminal viewport.
func (r *Renderer) TargetSize(desktopWidth, desktopHeight, width, height int) (int, int) {
	_, _, _, _, contentWidth, contentHeight, err := r.layout(desktopWidth, desktopHeight, width, height)
	if err != nil {
		return 1, 1
	}
	return contentWidth, contentHeight
}

// Render lays out the frame in terminal pixel space and splits it into tiles.
func (r *Renderer) Render(frame *capture.Frame, width, height int) *RenderedFrame {
	viewport, pixelViewport, cellWidth, cellHeight, imageWidth, imageHeight, err :=
		r.layout(frame.Width(), frame.Height(), width, height)
	if err != nil {
		// Unreachable: dimensions are validated before render calls.
		viewport = render.Viewport{Width: 1, Height: 1, DesktopWidth: 1, DesktopHeight: 1}
		pixelViewport = PixelViewport{Width: 1, Height: 1, DesktopWidth: 1, DesktopHeight: 1}
		cellWidth, cellHeight = 1, 1
		imageWidth, imageHeight = 1, 1
	}
	cellColumns := viewport.Width
	cellRows := viewport.Height
	left := viewport.X
	top := viewport.Y

	var content *image.RGBA
	var contentRGB []byte
	if frame.RGB != nil && frame.RGBWidth == imageWidth && frame.RGBHeight == imageHeight {
		contentRGB = frame.RGB
	} else {
		source := frame.RGBAImage()
		switch {
		case source.Rect.Empty():
			content = image.NewRGBA(image.Rect(0, 0, imageWidth, imageHeight))
		case source.Rect.Dx() == imageWidth && source.Rect.Dy() == imageHeight:
			content = source
		default:
			content = xshm.Scale(source, imageWidth, imageHeight)
		}
	}

	tileTargetPixels := min(TileTargetPixels, max(80, min(imageWidth, imageHeight)/2))
	tileColumns := max(1, tileTargetPixels/cellWidth)
	tileRows := max(1, tileTargetPixels/cellHeight)
	// Content pixels per grid cell; tiles are cropped from the content image
	// but placed by cell span so the terminal scales them.
	contentCellWidth := float64(imageWidth) / float64(cellColumns)
	contentCellHeight := float64(imageHeight) / float64(cellRows)
	var tiles []Tile
	tileY := 0
	for row := 0; row < cellRows; row += tileRows {
		rowsHere := min(tileRows, cellRows-row)
		tileX := 0
		for column := 0; column < cellColumns; column += tileColumns {
			columnsHere := min(tileColumns, cellColumns-column)
			x0 := pyRound(float64(column) * contentCellWidth)
			x1 := pyRound(float64(column+columnsHere) * contentCellWidth)
			y0 := pyRound(float64(row) * contentCellHeight)
			y1 := pyRound(float64(row+rowsHere) * contentCellHeight)
			tiles = append(tiles, Tile{
				ImageID:     ImageIDBase + tileY*1024 + tileX,
				Column:      left + column,
				Row:         top + row,
				Width:       x1 - x0,
				Height:      y1 - y0,
				CellColumns: columnsHere,
				CellRows:    rowsHere,
			})
			tileX++
		}
		tileY++
	}
	return &RenderedFrame{
		TerminalWidth:  width,
		TerminalHeight: height,
		Viewport:       viewport,
		PixelViewport:  pixelViewport,
		CellWidth:      cellWidth,
		CellHeight:     cellHeight,
		Image:          content,
		RGB:            contentRGB,
		RGBWidth:       imageWidth,
		RGBHeight:      imageHeight,
		Tiles:          tiles,
	}
}

// MaterializeTile crops the tile's RGB pixels out of the frame content and
// stamps its content digest.
func MaterializeTile(frame *RenderedFrame, tile Tile) Tile {
	contentWidth, contentHeight := frame.contentSize()
	contentCellWidth := float64(contentWidth) / float64(frame.Viewport.Width)
	contentCellHeight := float64(contentHeight) / float64(frame.Viewport.Height)
	x := pyRound(float64(tile.Column-frame.Viewport.X) * contentCellWidth)
	y := pyRound(float64(tile.Row-frame.Viewport.Y) * contentCellHeight)
	var rgb []byte
	if frame.RGB != nil {
		rgb = cropRGB24(frame.RGB, contentWidth, x, y, tile.Width, tile.Height)
	} else {
		rgb = cropRGB(frame.Image, x, y, tile.Width, tile.Height)
	}
	tile.Digest = capture.DigestPixels(rgb)
	tile.RGB = rgb
	return tile
}

// cropRGB24 copies a packed RGB24 rectangle out of a packed RGB24 frame.
func cropRGB24(src []byte, srcWidth, x, y, width, height int) []byte {
	rgb := make([]byte, 0, width*height*3)
	for row := y; row < y+height; row++ {
		start := (row*srcWidth + x) * 3
		rgb = append(rgb, src[start:start+width*3]...)
	}
	return rgb
}

// cropRGB extracts a packed RGB24 rectangle from the content image.
func cropRGB(img *image.RGBA, x, y, width, height int) []byte {
	rgb := make([]byte, 0, width*height*3)
	for row := y; row < y+height; row++ {
		base := img.PixOffset(x, row)
		for column := 0; column < width; column++ {
			offset := base + column*4
			rgb = append(rgb, img.Pix[offset], img.Pix[offset+1], img.Pix[offset+2])
		}
	}
	return rgb
}

// differenceBounds returns the bounding box of per-pixel RGB differences
// between two same-sized images; changed is false when they are identical.
func differenceBounds(a, b *image.RGBA) (left, top, right, bottom int, changed bool) {
	width := a.Rect.Dx()
	height := a.Rect.Dy()
	left, top, right, bottom = width, height, 0, 0
	for y := 0; y < height; y++ {
		rowA := a.PixOffset(0, y)
		rowB := b.PixOffset(0, y)
		for x := 0; x < width; x++ {
			offset := x * 4
			if a.Pix[rowA+offset] != b.Pix[rowB+offset] ||
				a.Pix[rowA+offset+1] != b.Pix[rowB+offset+1] ||
				a.Pix[rowA+offset+2] != b.Pix[rowB+offset+2] {
				changed = true
				if x < left {
					left = x
				}
				if x >= right {
					right = x + 1
				}
				if y < top {
					top = y
				}
				if y >= bottom {
					bottom = y + 1
				}
			}
		}
	}
	return left, top, right, bottom, changed
}

// regionChanged reports whether any pixel inside the box differs.
func regionChanged(a, b *image.RGBA, x0, y0, x1, y1 int) bool {
	for y := y0; y < y1; y++ {
		rowA := a.PixOffset(x0, y)
		rowB := b.PixOffset(x0, y)
		for x := x0; x < x1; x++ {
			offset := (x - x0) * 4
			if a.Pix[rowA+offset] != b.Pix[rowB+offset] ||
				a.Pix[rowA+offset+1] != b.Pix[rowB+offset+1] ||
				a.Pix[rowA+offset+2] != b.Pix[rowB+offset+2] {
				return true
			}
		}
	}
	return false
}

// differenceBoundsRGB24 is differenceBounds for packed RGB24 frames.
func differenceBoundsRGB24(a, b []byte, width, height int) (left, top, right, bottom int, changed bool) {
	left, top, right, bottom = width, height, 0, 0
	for y := 0; y < height; y++ {
		row := y * width * 3
		for x := 0; x < width; x++ {
			offset := row + x*3
			if a[offset] != b[offset] ||
				a[offset+1] != b[offset+1] ||
				a[offset+2] != b[offset+2] {
				changed = true
				if x < left {
					left = x
				}
				if x >= right {
					right = x + 1
				}
				if y < top {
					top = y
				}
				if y >= bottom {
					bottom = y + 1
				}
			}
		}
	}
	return left, top, right, bottom, changed
}

// regionChangedRGB24 is regionChanged for packed RGB24 frames.
func regionChangedRGB24(a, b []byte, width, x0, y0, x1, y1 int) bool {
	for y := y0; y < y1; y++ {
		row := y * width * 3
		for x := x0; x < x1; x++ {
			offset := row + x*3
			if a[offset] != b[offset] ||
				a[offset+1] != b[offset+1] ||
				a[offset+2] != b[offset+2] {
				return true
			}
		}
	}
	return false
}

// Diff computes the changed tiles between two rendered frames, escalating to
// a full update when the changed area crosses the replace threshold.
func (r *Renderer) Diff(previous, current *RenderedFrame) FrameUpdate {
	if previous == current {
		return FrameUpdate{Kind: render.UpdateUnchanged, Frame: current}
	}
	if previous == nil ||
		previous.TerminalWidth != current.TerminalWidth ||
		previous.TerminalHeight != current.TerminalHeight ||
		previous.PixelViewport != current.PixelViewport ||
		previous.CellWidth != current.CellWidth ||
		previous.CellHeight != current.CellHeight ||
		len(previous.Tiles) != len(current.Tiles) {
		return FrameUpdate{Kind: render.UpdateFull, Frame: current, Changes: current.Tiles}
	}
	previousWidth, previousHeight := previous.contentSize()
	currentWidth, currentHeight := current.contentSize()
	if previousWidth != currentWidth || previousHeight != currentHeight {
		return FrameUpdate{Kind: render.UpdateFull, Frame: current, Changes: current.Tiles}
	}
	// A mid-session switch between RGBA and RGB24 content repaints once.
	if (previous.RGB == nil) != (current.RGB == nil) {
		return FrameUpdate{Kind: render.UpdateFull, Frame: current, Changes: current.Tiles}
	}
	var left, top, right, bottom int
	var changed bool
	if current.RGB != nil {
		left, top, right, bottom, changed = differenceBoundsRGB24(previous.RGB, current.RGB, currentWidth, currentHeight)
	} else {
		left, top, right, bottom, changed = differenceBounds(previous.Image, current.Image)
	}
	if !changed {
		return FrameUpdate{Kind: render.UpdateUnchanged, Frame: current}
	}
	totalPixels := currentWidth * currentHeight
	contentCellWidth := float64(currentWidth) / float64(current.Viewport.Width)
	contentCellHeight := float64(currentHeight) / float64(current.Viewport.Height)
	changedPixels := 0
	var changes []Tile
	for _, tile := range current.Tiles {
		x := pyRound(float64(tile.Column-current.Viewport.X) * contentCellWidth)
		y := pyRound(float64(tile.Row-current.Viewport.Y) * contentCellHeight)
		if x >= right || y >= bottom || x+tile.Width <= left || y+tile.Height <= top {
			continue
		}
		tileChanged := false
		if current.RGB != nil {
			tileChanged = regionChangedRGB24(previous.RGB, current.RGB, currentWidth, x, y, x+tile.Width, y+tile.Height)
		} else {
			tileChanged = regionChanged(previous.Image, current.Image, x, y, x+tile.Width, y+tile.Height)
		}
		if !tileChanged {
			continue
		}
		changes = append(changes, MaterializeTile(current, tile))
		changedPixels += tile.Width * tile.Height
		if float64(changedPixels)*100 >= float64(totalPixels)*FullReplaceThreshold {
			return FrameUpdate{Kind: render.UpdateFull, Frame: current, Changes: current.Tiles}
		}
	}
	if len(changes) == 0 {
		return FrameUpdate{Kind: render.UpdateUnchanged, Frame: current}
	}
	return FrameUpdate{Kind: render.UpdateDelta, Frame: current, Changes: changes}
}
