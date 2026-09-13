package ansi

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/rylena/sshdesk-go/internal/render"
)

const csi = "\x1b["

// ANSI16Palette is the fixed xterm 16-color palette used for nearest-color
// quantization.
var ANSI16Palette = [16]render.RGB{
	{R: 0, G: 0, B: 0},
	{R: 205, G: 49, B: 49},
	{R: 13, G: 188, B: 121},
	{R: 229, G: 229, B: 16},
	{R: 36, G: 114, B: 200},
	{R: 188, G: 63, B: 188},
	{R: 17, G: 168, B: 205},
	{R: 229, G: 229, B: 229},
	{R: 102, G: 102, B: 102},
	{R: 241, G: 76, B: 76},
	{R: 35, G: 209, B: 139},
	{R: 245, G: 245, B: 67},
	{R: 59, G: 142, B: 234},
	{R: 214, G: 112, B: 214},
	{R: 41, G: 184, B: 219},
	{R: 255, G: 255, B: 255},
}

// Writer encodes rendered cells for a terminal.
type Writer struct {
	Capabilities render.Capabilities
	title        string

	mu       sync.Mutex
	cache256 map[render.RGB]int
	cache16  map[render.RGB]int
}

func NewWriter(capabilities render.Capabilities, title string) *Writer {
	if title == "" {
		title = "SSHDESK"
	}
	filtered := make([]rune, 0, len(title))
	for _, character := range title {
		if character >= 0x20 && character != 0x7F {
			filtered = append(filtered, character)
		}
	}
	cleaned := string(filtered)
	runes := []rune(cleaned)
	if len(runes) > 255 {
		cleaned = string(runes[:255])
	}
	return &Writer{
		Capabilities: capabilities,
		title:        cleaned,
		cache256:     make(map[render.RGB]int),
		cache16:      make(map[render.RGB]int),
	}
}

func (w *Writer) Close() {}

func (w *Writer) titleEnter() string {
	// Xterm's title stack lets SSHDESK restore the client's previous title.
	return csi + "22;0t" + "\x1b]2;" + w.title + "\x1b\\"
}

// TitleEnter exposes the title push/set sequence for the Kitty writer.
func (w *Writer) TitleEnter() string { return w.titleEnter() }

func titleLeave() string {
	return csi + "23;0t"
}

// TitleLeave exposes the title pop sequence for the Kitty writer.
func TitleLeave() string { return titleLeave() }

func distanceSquared(color render.RGB, r, g, b int) int {
	dr := int(color.R) - r
	dg := int(color.G) - g
	db := int(color.B) - b
	return dr*dr + dg*dg + db*db
}

// Quantize256 maps an RGB color to an xterm 256-color index, choosing between
// the 6x6x6 color cube and the grayscale ramp by squared distance.
func Quantize256(color render.RGB) int {
	cubeR := int(math_round(float64(color.R) / 255 * 5))
	cubeG := int(math_round(float64(color.G) / 255 * 5))
	cubeB := int(math_round(float64(color.B) / 255 * 5))
	cubeValue := func(v int) int {
		if v == 0 {
			return 0
		}
		return 55 + v*40
	}
	cubeDistance := distanceSquared(color, cubeValue(cubeR), cubeValue(cubeG), cubeValue(cubeB))
	average := (float64(color.R) + float64(color.G) + float64(color.B)) / 3
	grayLevel := min(23, max(0, int(math_round((average-8)/10))))
	gray := 8 + grayLevel*10
	grayDistance := distanceSquared(color, gray, gray, gray)
	if grayDistance < cubeDistance {
		return 232 + grayLevel
	}
	return 16 + 36*cubeR + 6*cubeG + cubeB
}

// Python round() uses banker's rounding; math.Round rounds half away from
// zero, so replicate round-half-even to match the Python behavior exactly.
func math_round(value float64) float64 {
	floor := math.Floor(value)
	diff := value - floor
	switch {
	case diff > 0.5:
		return floor + 1
	case diff < 0.5:
		return floor
	default:
		if int64(floor)%2 == 0 {
			return floor
		}
		return floor + 1
	}
}

// Quantize16 maps an RGB color to the nearest xterm 16-color palette index.
func Quantize16(color render.RGB) int {
	best := 0
	bestDistance := int(^uint(0) >> 1)
	for index, entry := range ANSI16Palette {
		distance := distanceSquared(color, int(entry.R), int(entry.G), int(entry.B))
		if distance < bestDistance {
			best = index
			bestDistance = distance
		}
	}
	return best
}

func (w *Writer) quantize256Cached(color render.RGB) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if index, ok := w.cache256[color]; ok {
		return index
	}
	index := Quantize256(color)
	if len(w.cache256) < 4096 {
		w.cache256[color] = index
	}
	return index
}

func (w *Writer) quantize16Cached(color render.RGB) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	if index, ok := w.cache16[color]; ok {
		return index
	}
	index := Quantize16(color)
	if len(w.cache16) < 4096 {
		w.cache16[color] = index
	}
	return index
}

// cellStyle returns the comparable style key, the SGR escape, and the glyph.
func (w *Writer) cellStyle(foreground, background render.RGB) (string, string, string) {
	glyph := "▀"
	if !w.Capabilities.Unicode {
		glyph = " "
		averaged := render.RGB{
			R: uint8((int(foreground.R) + int(background.R)) / 2),
			G: uint8((int(foreground.G) + int(background.G)) / 2),
			B: uint8((int(foreground.B) + int(background.B)) / 2),
		}
		foreground = averaged
		background = averaged
	}
	var style, escape string
	switch w.Capabilities.Color {
	case render.ColorTruecolor:
		style = fmt.Sprintf("tc:%d,%d,%d:%d,%d,%d",
			foreground.R, foreground.G, foreground.B, background.R, background.G, background.B)
		escape = fmt.Sprintf("%s38;2;%d;%d;%d;48;2;%d;%d;%dm", csi,
			foreground.R, foreground.G, foreground.B, background.R, background.G, background.B)
	case render.Color256:
		foregroundIndex := w.quantize256Cached(foreground)
		backgroundIndex := w.quantize256Cached(background)
		style = fmt.Sprintf("256:%d:%d", foregroundIndex, backgroundIndex)
		escape = fmt.Sprintf("%s38;5;%d;48;5;%dm", csi, foregroundIndex, backgroundIndex)
	default:
		foregroundIndex := w.quantize16Cached(foreground)
		backgroundIndex := w.quantize16Cached(background)
		foregroundCode := 30 + foregroundIndex
		if foregroundIndex >= 8 {
			foregroundCode = 90 + foregroundIndex - 8
		}
		backgroundCode := 40 + backgroundIndex
		if backgroundIndex >= 8 {
			backgroundCode = 100 + backgroundIndex - 8
		}
		style = fmt.Sprintf("16:%d:%d", foregroundIndex, backgroundIndex)
		escape = fmt.Sprintf("%s%d;%dm", csi, foregroundCode, backgroundCode)
	}
	return style, escape, glyph
}

// Enter switches the terminal into the SSHDESK alternate-screen state.
func (w *Writer) Enter() []byte {
	value := w.titleEnter() + csi + "?1049h" + csi + "?25l" + csi + "2J" + csi + "H"
	if w.Capabilities.Mouse {
		value += csi + "?1003h"
		if w.Capabilities.SGRMouse {
			value += csi + "?1006h"
		}
	}
	return []byte(value)
}

// Leave restores the terminal state saved by Enter.
func (w *Writer) Leave() []byte {
	return []byte(csi + "0m" + csi + "?25h" + csi + "?1006l" + csi + "?1003l" + csi + "?1049l" + titleLeave())
}

// Header draws the centered device title row.
func (w *Writer) Header(width int) []byte {
	width = max(1, width)
	label := " SSHDESK | " + strings.TrimPrefix(w.title, "SSHDESK - ") + " "
	line := center(label, width)
	_, escape, _ := w.cellStyle(render.RGB{R: 235, G: 240, B: 248}, render.RGB{R: 28, G: 38, B: 52})
	return []byte(fmt.Sprintf("%s1;1H%s%s%s0m", csi, escape, line, csi))
}

func center(text string, width int) string {
	runes := []rune(text)
	if len(runes) > width {
		return string(runes[:width])
	}
	padding := width - len(runes)
	left := padding / 2
	right := padding - left
	return strings.Repeat(" ", left) + text + strings.Repeat(" ", right)
}

// Full encodes every cell of the frame.
func (w *Writer) Full(frame *render.RenderedFrame) []byte {
	var output strings.Builder
	output.WriteString(csi + "H")
	previous := ""
	first := true
	for row := 0; row < frame.TerminalHeight; row++ {
		fmt.Fprintf(&output, "%s%d;1H", csi, row+1)
		start := row * frame.TerminalWidth
		for _, cell := range frame.Cells[start : start+frame.TerminalWidth] {
			style, escape, glyph := w.cellStyle(cell.Foreground, cell.Background)
			if first || style != previous {
				output.WriteString(escape)
				previous = style
				first = false
			}
			output.WriteString(glyph)
		}
	}
	output.WriteString(csi + "0m")
	return []byte(output.String())
}

// Delta encodes only changed cells, emitting cursor positions only when the
// changes are not contiguous.
func (w *Writer) Delta(update render.FrameUpdate) []byte {
	var output strings.Builder
	previous := ""
	first := true
	previousIndex := -2
	width := update.Frame.TerminalWidth
	for _, change := range update.Changes {
		row, column := change.Index/width, change.Index%width
		if change.Index != previousIndex+1 || column == 0 {
			fmt.Fprintf(&output, "%s%d;%dH", csi, row+1, column+1)
		}
		style, escape, glyph := w.cellStyle(change.Cell.Foreground, change.Cell.Background)
		if first || style != previous {
			output.WriteString(escape)
			previous = style
			first = false
		}
		output.WriteString(glyph)
		previousIndex = change.Index
	}
	output.WriteString(csi + "0m")
	return []byte(output.String())
}

// Update encodes a frame update; UNCHANGED produces no bytes.
func (w *Writer) Update(update render.FrameUpdate) []byte {
	switch update.Kind {
	case render.UpdateFull:
		return w.Full(update.Frame)
	case render.UpdateDelta:
		return w.Delta(update)
	default:
		return nil
	}
}

// Cursor draws or hides the remote cursor position.
func (w *Writer) Cursor(frame *render.RenderedFrame, remoteX, remoteY int, visible bool) []byte {
	if !visible {
		return []byte(csi + "?25l")
	}
	viewport := frame.Viewport
	x := viewport.X + min(viewport.Width-1, max(0, remoteX*viewport.Width/viewport.DesktopWidth))
	y := viewport.Y + min(viewport.Height-1, max(0, remoteY*viewport.Height/viewport.DesktopHeight))
	return []byte(fmt.Sprintf("%s%d;%dH%s?25h", csi, y+1, x+1, csi))
}

// LatencyProbe is the ANSI Device Status Report; compatible terminals reply
// with CSI row;column R.
func LatencyProbe() []byte {
	return []byte(csi + "6n")
}
