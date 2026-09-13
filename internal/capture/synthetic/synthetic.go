// Package synthetic produces deterministic synthetic desktop frames for
// tests, demos, and benchmarks.
package synthetic

import (
	"image"
	"math"
	"time"

	"github.com/rylena/sshdesk-go/internal/capture"
)

var (
	colorBackground = rgb(18, 22, 30)
	colorBar        = rgb(35, 42, 55)
	colorBarText    = rgb(230, 235, 245)
	colorWindow     = rgb(48, 57, 73)
	colorSubWindow  = rgb(43, 108, 176)
	colorSubText    = rgb(255, 255, 255)
	colorDot        = rgb(238, 127, 74)
)

func rgb(r, g, b uint8) [3]uint8 { return [3]uint8{r, g, b} }

// Capture is a deterministic screen source.
type Capture struct {
	width       int
	height      int
	animate     bool
	frameNumber int
}

// NewCapture creates a synthetic desktop of the given size.
func NewCapture(width, height int, animate bool) *Capture {
	return &Capture{width: width, height: height, animate: animate}
}

func (c *Capture) Size() (int, int) { return c.width, c.height }

// BackendName mirrors the Python class name used in status output.
func (c *Capture) BackendName() string { return "SyntheticCapture" }

func (c *Capture) CursorPosition() (int, int, bool) { return 0, 0, false }

func (c *Capture) SetTargetSize(width, height int) error { return nil }

func (c *Capture) SetFrameRate(framesPerSecond float64) error { return nil }

func (c *Capture) Close() {}

func setPixel(img *image.RGBA, x, y int, color [3]uint8) {
	if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
		return
	}
	i := img.PixOffset(x, y)
	img.Pix[i] = color[0]
	img.Pix[i+1] = color[1]
	img.Pix[i+2] = color[2]
	img.Pix[i+3] = 0xFF
}

// fillRect fills the inclusive rectangle (x0, y0)-(x1, y1), mirroring
// PIL's ImageDraw.rectangle semantics.
func fillRect(img *image.RGBA, x0, y0, x1, y1 int, color [3]uint8) {
	width := img.Rect.Dx()
	height := img.Rect.Dy()
	x0 = max(0, min(x0, width-1))
	x1 = max(0, min(x1, width-1))
	y0 = max(0, min(y0, height-1))
	y1 = max(0, min(y1, height-1))
	if x0 > x1 || y0 > y1 {
		return
	}
	for y := y0; y <= y1; y++ {
		i := img.PixOffset(x0, y)
		for x := x0; x <= x1; x++ {
			img.Pix[i] = color[0]
			img.Pix[i+1] = color[1]
			img.Pix[i+2] = color[2]
			img.Pix[i+3] = 0xFF
			i += 4
		}
	}
}

func fillEllipse(img *image.RGBA, x0, y0, x1, y1 int, color [3]uint8) {
	if x1 < x0 || y1 < y0 {
		return
	}
	cx := float64(x0+x1) / 2
	cy := float64(y0+y1) / 2
	rx := float64(x1-x0) / 2
	ry := float64(y1-y0) / 2
	if rx <= 0 {
		for y := y0; y <= y1; y++ {
			setPixel(img, x0, y, color)
		}
		return
	}
	if ry <= 0 {
		for x := x0; x <= x1; x++ {
			setPixel(img, x, y0, color)
		}
		return
	}
	for y := y0; y <= y1; y++ {
		dy := (float64(y) - cy) / ry
		span := rx * math.Sqrt(max(0, 1-dy*dy))
		for x := int(cx - span); x <= int(cx+span); x++ {
			setPixel(img, x, y, color)
		}
	}
}

// glyphs is a minimal 5x7 block font covering the synthetic desktop labels.
var glyphs = map[rune][7]string{
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'C': {"01110", "10001", "10000", "10000", "10000", "10001", "01110"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"01110", "00100", "00100", "00100", "00100", "00100", "01110"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	' ': {"00000", "00000", "00000", "00000", "00000", "00000", "00000"},
}

func drawText(img *image.RGBA, x, y int, text string, color [3]uint8) {
	cursor := x
	for _, character := range text {
		if character >= 'a' && character <= 'z' {
			character = character - 'a' + 'A'
		}
		glyph, known := glyphs[character]
		if !known {
			cursor += 6
			continue
		}
		for row, bits := range glyph {
			for column, on := range bits {
				if on == '1' {
					setPixel(img, cursor+column, y+row, color)
				}
			}
		}
		cursor += 6
	}
}

func (c *Capture) Capture() (*capture.Frame, error) {
	img := image.NewRGBA(image.Rect(0, 0, c.width, c.height))
	fillRect(img, 0, 0, c.width-1, c.height-1, colorBackground)
	barHeight := max(12, c.height/18)
	fillRect(img, 0, 0, c.width, barHeight, colorBar)
	drawText(img, max(2, c.width/100), max(1, barHeight/4), "SSHDESK", colorBarText)
	marginX := max(4, c.width/20)
	marginY := max(barHeight+4, c.height/8)
	fillRect(img, marginX, marginY, c.width-marginX, c.height-marginY/2, colorWindow)
	fillRect(img, marginX*2, marginY+4, c.width/2, c.height/2, colorSubWindow)
	drawText(img, marginX*2+4, marginY+8, "Terminal desktop", colorSubText)
	if c.animate {
		radius := max(3, min(c.width, c.height)/30)
		x := marginX + (c.frameNumber*13)%max(1, c.width-2*marginX-2*radius)
		y := max(barHeight, c.height-marginY-2*radius)
		fillEllipse(img, x, y, x+2*radius, y+2*radius, colorDot)
	}
	c.frameNumber++
	return &capture.Frame{
		Image:      img,
		CapturedNs: time.Now().UnixNano(),
	}, nil
}
