package ansi

import (
	"bytes"
	"image"
	"testing"
	"time"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/capture/synthetic"
	"github.com/rylena/sshdesk-go/internal/render"
)

func testWriter(color render.ColorMode, unicode bool) *Writer {
	return NewWriter(render.Capabilities{
		Term:     "test",
		Color:    color,
		Mouse:    true,
		SGRMouse: true,
		Unicode:  unicode,
	}, "SSHDESK")
}

func solidFrame(width, height int, color render.RGB) *capture.Frame {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = color.R
		img.Pix[i+1] = color.G
		img.Pix[i+2] = color.B
		img.Pix[i+3] = 0xFF
	}
	return &capture.Frame{Image: img, CapturedNs: time.Now().UnixNano()}
}

func TestFullDeltaAndUnchanged(t *testing.T) {
	renderer, err := NewRenderer(0.9, 0, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	source := synthetic.NewCapture(320, 180, false)
	frame, err := source.Capture()
	if err != nil {
		t.Fatal(err)
	}
	first := renderer.Render(frame, 40, 12)
	if got := renderer.Diff(nil, first).Kind; got != render.UpdateFull {
		t.Errorf("diff(nil) = %v, want FULL", got)
	}
	if got := renderer.Diff(first, first).Kind; got != render.UpdateUnchanged {
		t.Errorf("diff(same) = %v, want UNCHANGED", got)
	}

	changed := *frame
	changedImage := image.NewRGBA(frame.Image.Rect)
	copy(changedImage.Pix, frame.Image.Pix)
	offset := changedImage.PixOffset(160, 90)
	changedImage.Pix[offset] = 255
	changedImage.Pix[offset+1] = 0
	changedImage.Pix[offset+2] = 255
	changed.Image = changedImage
	second := renderer.Render(&changed, 40, 12)
	update := renderer.Diff(first, second)
	if update.Kind != render.UpdateDelta && update.Kind != render.UpdateUnchanged {
		t.Errorf("single pixel change = %v", update.Kind)
	}
}

func TestLargeChangeBecomesFull(t *testing.T) {
	renderer, err := NewRenderer(0.5, 0, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	black := renderer.Render(solidFrame(100, 100, render.RGB{}), 20, 10)
	white := renderer.Render(solidFrame(100, 100, render.RGB{R: 255, G: 255, B: 255}), 20, 10)
	if got := renderer.Diff(black, white).Kind; got != render.UpdateFull {
		t.Errorf("full inversion = %v, want FULL", got)
	}
}

func TestResizeForcesFull(t *testing.T) {
	renderer, err := NewRenderer(0.6, 0, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	source, err := synthetic.NewCapture(320, 180, false).Capture()
	if err != nil {
		t.Fatal(err)
	}
	first := renderer.Render(source, 80, 24)
	resized := renderer.Render(source, 100, 30)
	if got := renderer.Diff(first, resized).Kind; got != render.UpdateFull {
		t.Errorf("resize = %v, want FULL", got)
	}
	if resized.TerminalWidth != 100 || resized.TerminalHeight != 30 {
		t.Errorf("resized dims = %dx%d", resized.TerminalWidth, resized.TerminalHeight)
	}
}

func TestAspectRatioLetterboxes(t *testing.T) {
	viewport, err := CalculateViewport(1920, 1080, 80, 40, 0, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	if viewport.Width != 80 {
		t.Errorf("width = %d, want 80", viewport.Width)
	}
	if viewport.Height >= 40 {
		t.Errorf("height = %d, want < 40", viewport.Height)
	}
	if viewport.Y <= 0 {
		t.Errorf("y = %d, want > 0", viewport.Y)
	}
}

func TestRendererReservesDeviceHeaderRow(t *testing.T) {
	renderer, err := NewRenderer(0.6, 1, 1.0)
	if err != nil {
		t.Fatal(err)
	}
	source, err := synthetic.NewCapture(1920, 1080, false).Capture()
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderer.Render(source, 80, 24)
	if rendered.Viewport.Y < 1 {
		t.Errorf("viewport.y = %d, want >= 1", rendered.Viewport.Y)
	}
}

func TestRenderScaleReducesCaptureTargetsWithoutChangingDesktopMapping(t *testing.T) {
	full, _ := NewRenderer(0.6, 1, 1.0)
	smooth, _ := NewRenderer(0.6, 1, 0.5)
	fullW, fullH := full.TargetSize(1920, 1080, 120, 40)
	smoothW, smoothH := smooth.TargetSize(1920, 1080, 120, 40)
	if smoothW*smoothH >= fullW*fullH {
		t.Errorf("smooth target %dx%d not smaller than %dx%d", smoothW, smoothH, fullW, fullH)
	}
	rendered := smooth.Render(&capture.Frame{
		Image:         solidFrame(smoothW, smoothH, render.RGB{}).Image,
		CapturedNs:    1,
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, 120, 40)
	if rendered.Viewport.DesktopWidth != 1920 || rendered.Viewport.DesktopHeight != 1080 {
		t.Errorf("desktop mapping = %dx%d", rendered.Viewport.DesktopWidth, rendered.Viewport.DesktopHeight)
	}
	if rendered.Viewport.X <= 0 {
		t.Errorf("viewport.x = %d, want > 0", rendered.Viewport.X)
	}
}

func TestViewportValidation(t *testing.T) {
	if _, err := CalculateViewport(1920, 1080, 0, 24, 0, 1.0); err == nil {
		t.Error("zero columns must fail")
	}
	if _, err := CalculateViewport(0, 1080, 80, 24, 0, 1.0); err == nil {
		t.Error("zero desktop width must fail")
	}
	if _, err := NewRenderer(0.6, 17, 1.0); err == nil {
		t.Error("margin 17 must fail")
	}
	if _, err := NewRenderer(0.6, 1, 0.1); err == nil {
		t.Error("scale 0.1 must fail")
	}
}

func TestDeviceHeaderAndTerminalTitleAreRestored(t *testing.T) {
	writer := NewWriter(render.Capabilities{
		Term:     "test",
		Color:    render.ColorTruecolor,
		Mouse:    true,
		SGRMouse: true,
		Unicode:  true,
	}, "SSHDESK - laptop")
	if !bytes.Contains(writer.Enter(), []byte("\x1b[22;0t\x1b]2;SSHDESK - laptop")) {
		t.Errorf("enter = %q", writer.Enter())
	}
	if !bytes.Contains(writer.Header(80), []byte("SSHDESK | laptop")) {
		t.Errorf("header = %q", writer.Header(80))
	}
	if !bytes.Contains(writer.Leave(), []byte("\x1b[23;0t")) {
		t.Errorf("leave = %q", writer.Leave())
	}
	if !bytes.Contains(writer.Enter(), []byte("\x1b[?1049h")) {
		t.Error("enter must switch to the alternate screen")
	}
	if !bytes.Contains(writer.Enter(), []byte("\x1b[?1006h")) {
		t.Error("enter must enable SGR mouse")
	}
	if !bytes.Contains(writer.Leave(), []byte("\x1b[?1049l")) {
		t.Error("leave must restore the primary screen")
	}
}

func TestDeltaWriterEmitsOneGlyphPerChange(t *testing.T) {
	renderer, _ := NewRenderer(1.0, 0, 1.0)
	source := synthetic.NewCapture(100, 100, false)
	frame, err := source.Capture()
	if err != nil {
		t.Fatal(err)
	}
	first := renderer.Render(frame, 10, 5)
	cells := make([]render.Cell, len(first.Cells))
	copy(cells, first.Cells)
	cells[0] = render.Cell{
		Foreground: render.RGB{R: 1, G: 2, B: 3},
		Background: render.RGB{R: 4, G: 5, B: 6},
	}
	second := &render.RenderedFrame{
		TerminalWidth:  first.TerminalWidth,
		TerminalHeight: first.TerminalHeight,
		Viewport:       first.Viewport,
		Cells:          cells,
	}
	update := renderer.Diff(first, second)
	encoded := testWriter(render.ColorTruecolor, true).Delta(update)
	if got := bytes.Count(encoded, []byte("▀")); got != 1 {
		t.Errorf("glyph count = %d, want 1 (%q)", got, encoded)
	}
}

func TestColorFallbacks(t *testing.T) {
	renderer, _ := NewRenderer(0.6, 0, 1.0)
	frame, err := synthetic.NewCapture(100, 100, false).Capture()
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderer.Render(frame, 10, 5)
	truecolor := testWriter(render.ColorTruecolor, true).Full(rendered)
	ansi256 := testWriter(render.Color256, true).Full(rendered)
	ansi16 := testWriter(render.Color16, true).Full(rendered)
	ascii := testWriter(render.Color16, false).Full(rendered)
	if !bytes.Contains(truecolor, []byte("38;2;")) {
		t.Error("truecolor must use 38;2")
	}
	if !bytes.Contains(ansi256, []byte("38;5;")) {
		t.Error("256-color must use 38;5")
	}
	if bytes.Contains(ansi16, []byte("38;5;")) {
		t.Error("16-color must not use 38;5")
	}
	if bytes.Contains(ascii, []byte("▀")) {
		t.Error("ASCII mode must not use half blocks")
	}
}

func TestQuantize256(t *testing.T) {
	if got := Quantize256(render.RGB{}); got != 16 {
		t.Errorf("black = %d, want 16", got)
	}
	if got := Quantize256(render.RGB{R: 255, G: 255, B: 255}); got != 231 {
		t.Errorf("white = %d, want 231", got)
	}
	if got := Quantize256(render.RGB{R: 128, G: 128, B: 128}); got != 244 {
		t.Errorf("mid gray = %d, want 244", got)
	}
	if got := Quantize256(render.RGB{R: 255, G: 0, B: 0}); got != 196 {
		t.Errorf("red = %d, want 196", got)
	}
}

func TestQuantize16(t *testing.T) {
	if got := Quantize16(render.RGB{R: 255, G: 255, B: 255}); got != 15 {
		t.Errorf("white = %d, want 15", got)
	}
	if got := Quantize16(render.RGB{}); got != 0 {
		t.Errorf("black = %d, want 0", got)
	}
	if got := Quantize16(render.RGB{R: 240, G: 70, B: 70}); got != 9 {
		t.Errorf("bright red = %d, want 9", got)
	}
}

func TestOverlayLayout(t *testing.T) {
	writer := testWriter(render.ColorTruecolor, true)
	encoded := writer.Overlay(render.StatsSnapshot{
		FPS:            30.0,
		CapturedFPS:    30.0,
		TerminalWidth:  80,
		TerminalHeight: 24,
		BytesSent:      2048,
		BytesReceived:  1024,
		LatencyMs:      12.5,
		FullFrames:     3,
		DeltaFrames:    42,
		DroppedFrames:  1,
		RemoteWidth:    1280,
		RemoteHeight:   720,
	})
	for _, want := range []string{"\x1b[2;1H", "\x1b[3;1H", "\x1b[4;1H", "\x1b[5;1H", "SSHDESK", "RTT", "term 80x24", "remote 1280x720"} {
		if !bytes.Contains(encoded, []byte(want)) {
			t.Errorf("overlay missing %q (%q)", want, encoded)
		}
	}
}

func TestCursorPlacement(t *testing.T) {
	writer := testWriter(render.ColorTruecolor, true)
	viewport := render.Viewport{X: 10, Y: 5, Width: 100, Height: 50, DesktopWidth: 1920, DesktopHeight: 1080}
	frame := &render.RenderedFrame{TerminalWidth: 120, TerminalHeight: 60, Viewport: viewport}
	if got := writer.Cursor(frame, 0, 0, false); !bytes.Equal(got, []byte("\x1b[?25l")) {
		t.Errorf("hidden cursor = %q", got)
	}
	encoded := writer.Cursor(frame, 960, 540, true)
	if !bytes.Contains(encoded, []byte("\x1b[31;61H")) || !bytes.HasSuffix(encoded, []byte("\x1b[?25h")) {
		t.Errorf("cursor = %q", encoded)
	}
}
