package kitty

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"regexp"
	"testing"

	"github.com/rylena/sshdesk-go/internal/capture"
	"github.com/rylena/sshdesk-go/internal/render"
)

var kittyCommandRE = regexp.MustCompile(`\x1b_G([^;]+);([^\x1b]*)\x1b\\`)

func testWriter(t *testing.T) *Writer {
	t.Helper()
	return NewWriter(testCapabilities(), testProbe(), "SSHDESK")
}

func testCapabilities() render.Capabilities {
	return render.Capabilities{
		Term:     "xterm-kitty",
		Color:    render.ColorTruecolor,
		Mouse:    true,
		SGRMouse: true,
		Unicode:  true,
	}
}

func reassemblePayload(t *testing.T, output []byte) []byte {
	t.Helper()
	var payload []byte
	for _, match := range kittyCommandRE.FindAllSubmatch(output, -1) {
		keys, data := match[1], match[2]
		if bytes.Contains(keys, []byte("a=T")) || bytes.HasPrefix(keys, []byte("m=")) {
			payload = append(payload, data...)
		}
	}
	if len(payload) == 0 {
		t.Fatal("no graphics payload found")
	}
	decoded, err := base64.StdEncoding.DecodeString(string(payload))
	if err != nil {
		t.Fatalf("base64 payload: %v", err)
	}
	return decoded
}

func TestWriterEmitsChunkedPalettedPNG(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	frame := renderer.Render(captureFrame(t, 160, 90), 20, 8)
	writer := testWriter(t)
	tile := MaterializeTile(frame, frame.Tiles[0])
	output := writer.placeRGB(tile)
	if !bytes.Contains(output, []byte("\x1b_Ga=T")) {
		t.Error("placement command missing")
	}
	if !bytes.Contains(output, []byte("f=100")) {
		t.Error("PNG format key missing")
	}
	if !bytes.Contains(output, []byte("q=1")) {
		t.Error("quiet key missing")
	}
	pngData := reassemblePayload(t, output)
	decoded, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		t.Fatalf("PNG decode: %v", err)
	}
	if decoded.Bounds().Dx() != tile.Width || decoded.Bounds().Dy() != tile.Height {
		t.Errorf("decoded size = %dx%d, want %dx%d",
			decoded.Bounds().Dx(), decoded.Bounds().Dy(), tile.Width, tile.Height)
	}
	rawEncoded := base64.StdEncoding.EncodeToString(tile.RGB)
	if len(pngData) >= len(rawEncoded) {
		t.Errorf("paletted PNG %d bytes must beat raw RGB %d bytes", len(pngData), len(rawEncoded))
	}
}

func TestWriterUsesOneCanvasForLargeUpdates(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	first := renderer.Render(captureFrame(t, 1280, 720), 120, 36)
	writer := testWriter(t)
	full := writer.Full(first)
	if got := bytes.Count(full, []byte("\x1b_Ga=T")); got != 1 {
		t.Errorf("full frame transmissions = %d, want 1", got)
	}
	if !bytes.Contains(full, []byte("z=-2")) {
		t.Error("canvas layer key z=-2 missing")
	}

	white := image.NewRGBA(image.Rect(0, 0, 1280, 720))
	for i := 0; i < len(white.Pix); i += 4 {
		white.Pix[i], white.Pix[i+1], white.Pix[i+2], white.Pix[i+3] = 255, 255, 255, 255
	}
	second := renderer.Render(&capture.Frame{Image: white, DesktopWidth: 1280, DesktopHeight: 720}, 120, 36)
	update := renderer.Diff(first, second)
	if update.ChangedPercentage() < 25.0 {
		t.Fatalf("changed percentage = %v, want >= 25", update.ChangedPercentage())
	}
	delta := writer.Update(update)
	if got := bytes.Count(delta, []byte("\x1b_Ga=T")); got != 1 {
		t.Errorf("large delta transmissions = %d, want 1 canvas", got)
	}
	if !bytes.Contains(delta, []byte("d=Z,z=-1")) {
		t.Error("obsolete tile cleanup d=Z,z=-1 missing")
	}
}

func TestWriterDeltaPlacesSmallChanges(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	source := captureFrame(t, 320, 180)
	first := renderer.Render(source, 80, 24)
	modified := image.NewRGBA(source.Image.Rect)
	copy(modified.Pix, source.Image.Pix)
	offset := modified.PixOffset(10, 10)
	modified.Pix[offset], modified.Pix[offset+1], modified.Pix[offset+2] = 255, 0, 255
	second := renderer.Render(&capture.Frame{Image: modified, DesktopWidth: 320, DesktopHeight: 180}, 80, 24)
	update := renderer.Diff(first, second)
	if update.Kind != render.UpdateDelta {
		t.Fatalf("kind = %v, want DELTA", update.Kind)
	}
	writer := testWriter(t)
	output := writer.Update(update)
	if !bytes.Contains(output, []byte("z=-1,f=100")) {
		t.Error("tile placement must use the tile layer z=-1")
	}
	if bytes.Contains(output, []byte("z=-2")) {
		t.Error("small delta must not replace the canvas")
	}
	// Synchronized-output capable probes wrap the batch.
	if !bytes.HasPrefix(output, []byte("\x1b[?2026h")) || !bytes.HasSuffix(output, []byte("\x1b[?2026l")) {
		t.Error("delta must be wrapped in synchronized output markers")
	}
}

func TestWriterEnterLeaveAndResetCanvas(t *testing.T) {
	writer := testWriter(t)
	enter := writer.Enter()
	for _, want := range [][]byte{
		[]byte("\x1b[?1049h"), []byte("\x1b[?25l"), []byte("\x1b[?7l"),
		[]byte("\x1b[?1003h"), []byte("\x1b[?1006h"), []byte("\x1b[?1016h"),
	} {
		if !bytes.Contains(enter, want) {
			t.Errorf("enter sequence missing %q", want)
		}
	}
	leave := writer.Leave()
	for _, want := range [][]byte{
		[]byte("\x1b_Ga=d,d=A,q=1"), []byte("\x1b[?1016l"), []byte("\x1b[?1003l"), []byte("\x1b[?1049l"),
	} {
		if !bytes.Contains(leave, want) {
			t.Errorf("leave sequence missing %q", want)
		}
	}
	reset := writer.ResetCanvas()
	if !bytes.Contains(reset, []byte("\x1b_Ga=d,d=A,q=1\x1b\\")) ||
		!bytes.Contains(reset, []byte("\x1b[2J\x1b[H")) {
		t.Error("reset canvas must delete all images and clear the screen")
	}
}

func TestWriterFullFrameAlternatesCanvasIDs(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	frame := renderer.Render(captureFrame(t, 160, 90), 20, 8)
	writer := testWriter(t)
	first := writer.Full(frame)
	second := writer.Full(frame)
	if !bytes.Contains(first, []byte("i=29952")) { // 0x7500
		t.Error("first canvas must use id 0x7500")
	}
	if !bytes.Contains(second, []byte("i=29953")) { // 0x7501
		t.Error("second canvas must use id 0x7501")
	}
	if !bytes.Contains(second, []byte("\x1b_Ga=d,d=I,i=29952,q=1")) {
		t.Error("second canvas must delete the previous one")
	}
}

func TestWriterCursorLifecycle(t *testing.T) {
	renderer := testRenderer(t, 0, 1.0)
	frame := renderer.Render(captureFrame(t, 320, 180), 80, 24)
	writer := testWriter(t)

	hidden := writer.Cursor(frame, 10, 10, false)
	want := []byte("\x1b_Ga=d,d=i,i=292352,p=1,q=2\x1b\\") // 0x47600
	if !bytes.Contains(hidden, want) {
		t.Errorf("hidden cursor = %q, want delete of %q", hidden, want)
	}

	first := writer.Cursor(frame, 160, 90, true)
	if !bytes.Contains(first, []byte("a=T,q=1,C=1,z=1,f=32,i=292352")) {
		t.Error("first visible cursor must transmit the RGBA sprite")
	}
	second := writer.Cursor(frame, 170, 95, true)
	if !bytes.Contains(second, []byte("a=p,q=2,C=1,z=1,i=292352,p=1,")) {
		t.Error("subsequent cursor moves must only re-place the sprite")
	}
	if bytes.Contains(second, []byte("f=32")) {
		t.Error("sprite must not be retransmitted")
	}
}

func TestGraphicsAreWrappedForTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux,1,0")
	writer := NewWriter(testCapabilities(), testProbe(), "SSHDESK")
	renderer := testRenderer(t, 0, 1.0)
	frame := renderer.Render(captureFrame(t, 160, 90), 20, 8)
	output := writer.placeRGB(MaterializeTile(frame, frame.Tiles[0]))
	if !bytes.HasPrefix(output, []byte("\x1bPtmux;\x1b\x1b_G")) {
		t.Errorf("tmux output must start with the DCS passthrough prefix: %q", output[:16])
	}
	if !bytes.Contains(output, []byte("\x1b\\")) {
		t.Error("tmux output must contain the ST terminator")
	}
}

func TestOctreeQuantizeKeepsFewColorsExact(t *testing.T) {
	width, height := 16, 16
	rgb := make([]byte, width*height*3)
	for i := 0; i < width*height; i++ {
		switch i % 4 {
		case 0:
			rgb[i*3], rgb[i*3+1], rgb[i*3+2] = 255, 0, 0
		case 1:
			rgb[i*3], rgb[i*3+1], rgb[i*3+2] = 0, 255, 0
		case 2:
			rgb[i*3], rgb[i*3+1], rgb[i*3+2] = 0, 0, 255
		default:
			rgb[i*3], rgb[i*3+1], rgb[i*3+2] = 255, 255, 255
		}
	}
	img := quantizePaletted(rgb, width, height)
	if len(img.Palette) != 4 {
		t.Fatalf("palette size = %d, want 4", len(img.Palette))
	}
	for pixel := 0; pixel < width*height; pixel++ {
		entry := img.Palette[img.Pix[pixel]]
		r, g, b, _ := entry.RGBA()
		if uint8(r>>8) != rgb[pixel*3] || uint8(g>>8) != rgb[pixel*3+1] || uint8(b>>8) != rgb[pixel*3+2] {
			t.Fatalf("pixel %d mapped to %v, want exact %v", pixel, entry, rgb[pixel*3:pixel*3+3])
		}
	}
}

func TestOctreeQuantizeCapsPalette(t *testing.T) {
	width, height := 64, 64
	rgb := make([]byte, width*height*3)
	for i := 0; i < width*height; i++ {
		rgb[i*3] = uint8(i * 7)
		rgb[i*3+1] = uint8(i * 13)
		rgb[i*3+2] = uint8(i * 29)
	}
	img := quantizePaletted(rgb, width, height)
	if len(img.Palette) > 128 {
		t.Fatalf("palette size = %d, want <= 128", len(img.Palette))
	}
	if len(img.Palette) == 0 {
		t.Fatal("palette must not be empty")
	}
	pngData, err := palettePNG(rgb, width, height)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(pngData)); err != nil {
		t.Fatalf("palette PNG must decode: %v", err)
	}
}
