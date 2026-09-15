package kitty

import (
	"bytes"
	"image"
	"testing"

	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/capture/synthetic"
	"github.com/rarnu/sshdesk-go/internal/render"
	"github.com/rarnu/sshdesk-go/internal/render/probe"
)

func testProbe() probe.GraphicsProbe {
	return probe.GraphicsProbe{
		KittyGraphics:      true,
		PixelMouse:         true,
		SynchronizedOutput: true,
		TextWidth:          800,
		TextHeight:         480,
		CellWidth:          10,
		CellHeight:         20,
	}
}

func testRenderer(t *testing.T, topMargin int, renderScale float64) *Renderer {
	t.Helper()
	renderer, err := NewRenderer(testProbe(), topMargin, renderScale)
	if err != nil {
		t.Fatal(err)
	}
	return renderer
}

func captureFrame(t *testing.T, width, height int) *capture.Frame {
	t.Helper()
	frame, err := synthetic.NewCapture(width, height, false).Capture()
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func TestNewRendererValidation(t *testing.T) {
	if _, err := NewRenderer(probe.GraphicsProbe{}, 0, 1.0); err == nil {
		t.Error("unusable probe must be rejected")
	}
	if _, err := NewRenderer(testProbe(), 17, 1.0); err == nil {
		t.Error("top margin above 16 must be rejected")
	}
	if _, err := NewRenderer(testProbe(), 0, 0.1); err == nil {
		t.Error("render scale below 0.25 must be rejected")
	}
}

func TestRendererReservesTheDeviceHeaderRow(t *testing.T) {
	frame := testRenderer(t, 1, 1.0).Render(captureFrame(t, 320, 180), 80, 24)
	if frame.Viewport.Y < 1 {
		t.Errorf("viewport y = %d, want >= 1", frame.Viewport.Y)
	}
	if frame.PixelViewport.Y < frame.CellHeight {
		t.Errorf("pixel viewport y = %d, want >= cell height %d",
			frame.PixelViewport.Y, frame.CellHeight)
	}
}

func TestRendererUsesTerminalPixelsAndTileDeltas(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	source := captureFrame(t, 320, 180)
	first := renderer.Render(source, 80, 24)
	if first.PixelViewport.Width <= first.TerminalWidth*2 {
		t.Errorf("pixel viewport width %d must exceed %d",
			first.PixelViewport.Width, first.TerminalWidth*2)
	}
	if first.PixelViewport.Height <= first.TerminalHeight*2 {
		t.Errorf("pixel viewport height %d must exceed %d",
			first.PixelViewport.Height, first.TerminalHeight*2)
	}
	if got := renderer.Diff(nil, first).Kind; got != render.UpdateFull {
		t.Errorf("diff(nil) = %v, want FULL", got)
	}
	if got := renderer.Diff(first, first).Kind; got != render.UpdateUnchanged {
		t.Errorf("diff(same) = %v, want UNCHANGED", got)
	}

	modified := image.NewRGBA(source.Image.Rect)
	copy(modified.Pix, source.Image.Pix)
	for y := 60; y < 70; y++ {
		for x := 110; x < 120; x++ {
			offset := modified.PixOffset(x, y)
			modified.Pix[offset] = 255
			modified.Pix[offset+1] = 0
			modified.Pix[offset+2] = 255
			modified.Pix[offset+3] = 255
		}
	}
	second := renderer.Render(&capture.Frame{
		Image:         modified,
		DesktopWidth:  320,
		DesktopHeight: 180,
	}, 80, 24)
	update := renderer.Diff(first, second)
	if update.Kind != render.UpdateDelta {
		t.Fatalf("small change kind = %v, want DELTA", update.Kind)
	}
	if len(update.Changes) == 0 {
		t.Error("DELTA must carry changed tiles")
	}
	if len(update.Changes) >= len(second.Tiles) {
		t.Errorf("changed tiles %d must be fewer than all %d", len(update.Changes), len(second.Tiles))
	}
	for _, tile := range update.Changes {
		if len(tile.Digest) != 8 || len(tile.RGB) != tile.Width*tile.Height*3 {
			t.Errorf("tile %d not materialized: digest %d bytes, rgb %d bytes",
				tile.ImageID, len(tile.Digest), len(tile.RGB))
		}
	}
}

func TestRendererUsesClientFPSFriendlyTileCount(t *testing.T) {
	rendered := testRenderer(t, 0, 1.0).Render(captureFrame(t, 1920, 1080), 120, 40)
	if len(rendered.Tiles) > 48 {
		t.Errorf("tile count = %d, want <= 48", len(rendered.Tiles))
	}
}

func TestRendererAcceptsPrescaledFrameWithDesktopCoordinates(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	targetW, targetH := renderer.TargetSize(1920, 1080, 80, 24)
	content := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	for i := 0; i < len(content.Pix); i += 4 {
		content.Pix[i] = 12
		content.Pix[i+1] = 34
		content.Pix[i+2] = 56
		content.Pix[i+3] = 255
	}
	rendered := renderer.Render(&capture.Frame{
		Image:         content,
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, 80, 24)
	// The placement grid is rounded to whole cells: 450 content pixels at
	// 20px cells place as 22 rows (440 pixels). The upstream Python test
	// expects the unrounded target size here and fails against its own
	// implementation; the port asserts the real layout.
	if rendered.PixelViewport.Width != targetW {
		t.Errorf("pixel viewport width = %d, want %d", rendered.PixelViewport.Width, targetW)
	}
	if rendered.PixelViewport.Height != 440 {
		t.Errorf("pixel viewport height = %d, want 440", rendered.PixelViewport.Height)
	}
	if rendered.PixelViewport.DesktopWidth != 1920 || rendered.PixelViewport.DesktopHeight != 1080 {
		t.Error("desktop coordinate space must be preserved")
	}
	if rendered.Image != content {
		t.Error("prescaled content must be used without resampling")
	}
}

func TestRenderScaleReducesCaptureTargetsWithoutChangingDesktopMapping(t *testing.T) {
	full := testRenderer(t, 0, 1.0)
	smooth := testRenderer(t, 0, 0.5)
	fullW, fullH := full.TargetSize(1920, 1080, 120, 40)
	smoothW, smoothH := smooth.TargetSize(1920, 1080, 120, 40)
	if smoothW*smoothH >= fullW*fullH {
		t.Errorf("render scale 0.5 target %dx%d must be smaller than %dx%d",
			smoothW, smoothH, fullW, fullH)
	}
	content := image.NewRGBA(image.Rect(0, 0, smoothW, smoothH))
	rendered := smooth.Render(&capture.Frame{
		Image:         content,
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, 120, 40)
	if rendered.PixelViewport.DesktopWidth != 1920 || rendered.PixelViewport.DesktopHeight != 1080 {
		t.Error("desktop coordinate space must be preserved")
	}
}

func TestRendererNeverUpscalesTheContentImage(t *testing.T) {
	// The placement grid may upscale a small desktop (the terminal scales the
	// placed cells by design), but the transmitted content pixels are always
	// capped at the desktop's native resolution times render_scale.
	rendered := testRenderer(t, 0, 1.0).Render(captureFrame(t, 320, 180), 200, 80)
	if rendered.Image.Rect.Dx() > 320 || rendered.Image.Rect.Dy() > 180 {
		t.Errorf("content image = %dx%d, must not exceed 320x180",
			rendered.Image.Rect.Dx(), rendered.Image.Rect.Dy())
	}
}

func TestTranslatePixelCoordinates(t *testing.T) {
	viewport := PixelViewport{X: 40, Y: 20, Width: 800, Height: 440, DesktopWidth: 1920, DesktopHeight: 1080}
	if _, _, ok := TranslatePixelCoordinates(39, 100, viewport); ok {
		t.Error("left of the viewport must not translate")
	}
	if _, _, ok := TranslatePixelCoordinates(100, 20+440, viewport); ok {
		t.Error("below the viewport must not translate")
	}
	x, y, ok := TranslatePixelCoordinates(40, 20, viewport)
	if !ok || x != 1 || y != 1 {
		t.Errorf("top-left pixel = (%d, %d, %v), want (1, 1, true)", x, y, ok)
	}
	x, y, ok = TranslatePixelCoordinates(839, 459, viewport)
	if !ok || x > 1919 || y > 1079 {
		t.Errorf("bottom-right pixel = (%d, %d), must stay inside the desktop", x, y)
	}
}

func TestDiffEscalatesToFullPastThreshold(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	first := renderer.Render(captureFrame(t, 1280, 720), 120, 36)
	white := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for i := 0; i < len(white.Pix); i += 4 {
		white.Pix[i] = 255
		white.Pix[i+1] = 255
		white.Pix[i+2] = 255
		white.Pix[i+3] = 255
	}
	second := renderer.Render(&capture.Frame{Image: white, DesktopWidth: 1280, DesktopHeight: 720}, 120, 36)
	update := renderer.Diff(first, second)
	if update.Kind != render.UpdateFull {
		t.Fatalf("large change kind = %v, want FULL", update.Kind)
	}
	if update.ChangedPercentage() < 25.0 {
		t.Errorf("changed percentage = %v, want >= 25", update.ChangedPercentage())
	}
	if len(update.Changes) != len(second.Tiles) {
		t.Error("FULL update must carry every tile")
	}
}

func TestRendererDiffsRGB24Content(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	targetW, targetH := renderer.TargetSize(1920, 1080, 80, 24)
	content := make([]byte, targetW*targetH*3)
	for i := 0; i < len(content); i += 3 {
		content[i] = 12
		content[i+1] = 34
		content[i+2] = 56
	}
	first := renderer.Render(&capture.Frame{
		RGB:           content,
		RGBWidth:      targetW,
		RGBHeight:     targetH,
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, 80, 24)
	if first.Image != nil {
		t.Error("RGB24 content must not be expanded to RGBA")
	}
	if string(first.RGB) != string(content) {
		t.Error("RGB24 content must be carried without resampling")
	}
	if got := renderer.Diff(nil, first).Kind; got != render.UpdateFull {
		t.Errorf("diff(nil) = %v, want FULL", got)
	}

	modified := bytes.Clone(content)
	for y := 20; y < 30; y++ {
		for x := 40; x < 50; x++ {
			offset := (y*targetW + x) * 3
			modified[offset] = 255
			modified[offset+1] = 0
			modified[offset+2] = 255
		}
	}
	second := renderer.Render(&capture.Frame{
		RGB:           modified,
		RGBWidth:      targetW,
		RGBHeight:     targetH,
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, 80, 24)
	update := renderer.Diff(first, second)
	if update.Kind != render.UpdateDelta {
		t.Fatalf("small RGB24 change kind = %v, want DELTA", update.Kind)
	}
	if len(update.Changes) == 0 || len(update.Changes) >= len(second.Tiles) {
		t.Fatalf("DELTA changes = %d of %d tiles", len(update.Changes), len(second.Tiles))
	}
	for _, tile := range update.Changes {
		if len(tile.Digest) != 8 || len(tile.RGB) != tile.Width*tile.Height*3 {
			t.Errorf("tile %d not materialized: digest %d bytes, rgb %d bytes",
				tile.ImageID, len(tile.Digest), len(tile.RGB))
		}
	}

	// A representation switch must repaint the whole canvas.
	rgba := renderer.Render(&capture.Frame{
		Image:         capture.RGB24ToRGBA(content, targetW, targetH),
		DesktopWidth:  1920,
		DesktopHeight: 1080,
	}, 80, 24)
	if got := renderer.Diff(second, rgba).Kind; got != render.UpdateFull {
		t.Errorf("RGB24 -> RGBA diff = %v, want FULL", got)
	}
}
