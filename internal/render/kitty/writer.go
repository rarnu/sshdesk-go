package kitty

import (
	"encoding/base64"
	"fmt"
	"image"
	"os"
	"runtime"
	"sync"

	"github.com/rarnu/sshdesk-go/internal/render"
	"github.com/rarnu/sshdesk-go/internal/render/ansi"
	"github.com/rarnu/sshdesk-go/internal/render/probe"
)

const csi = "\x1b["

// Writer encodes real RGB pixels with the Kitty graphics protocol. It embeds
// the ANSI writer for the shared title, header, and stats-overlay sequences.
type Writer struct {
	*ansi.Writer
	probe probe.GraphicsProbe

	cursorLoaded bool
	baseImageID  int
	tmux         bool
}

// NewWriter builds a Kitty graphics writer. The tmux passthrough mode is
// decided once from the environment, matching the Python implementation.
func NewWriter(capabilities render.Capabilities, p probe.GraphicsProbe, title string) *Writer {
	return &Writer{
		Writer: ansi.NewWriter(capabilities, title),
		probe:  p,
		tmux:   os.Getenv("TMUX") != "",
	}
}

func (w *Writer) graphics(sequence []byte) []byte {
	if !w.tmux {
		return sequence
	}
	return probe.TmuxPassthrough(sequence)
}

// ResetCanvas forgets all terminal-side images after a resize or screen
// reset.
func (w *Writer) ResetCanvas() []byte {
	w.baseImageID = 0
	w.cursorLoaded = false
	return append(w.graphics([]byte("\x1b_Ga=d,d=A,q=1\x1b\\")), []byte(csi+"2J"+csi+"H")...)
}

// Enter switches the terminal into the SSHDESK alternate-screen state.
func (w *Writer) Enter() []byte {
	value := w.TitleEnter() + csi + "?1049h" + csi + "?25l" + csi + "?7l"
	if w.Capabilities.Mouse {
		value += csi + "?1003h" + csi + "?1006h"
		if w.probe.PixelMouse {
			value += csi + "?1016h"
		}
	}
	return []byte(value + csi + "2J" + csi + "H")
}

// Leave restores the terminal state saved by Enter.
func (w *Writer) Leave() []byte {
	w.cursorLoaded = false
	w.baseImageID = 0
	terminalReset := csi + "0m" + csi + "?25h" + csi + "?1016l" + csi + "?1006l" +
		csi + "?1003l" + csi + "?7h" + csi + "?1049l" + ansi.TitleLeave()
	return append(w.graphics([]byte("\x1b_Ga=d,d=A,q=1\x1b\\")), []byte(terminalReset)...)
}

func chunks(payload []byte) [][]byte {
	var parts [][]byte
	for index := 0; index < len(payload); index += ChunkSize {
		parts = append(parts, payload[index:min(index+ChunkSize, len(payload))])
	}
	return parts
}

// appendTransmission emits one chunked Kitty graphics transmission. prefix is
// the first command's key list without the trailing m= continuation key.
func (w *Writer) appendTransmission(output []byte, prefix string, encoded []byte) []byte {
	parts := chunks(encoded)
	var first []byte
	if len(parts) > 0 {
		first = parts[0]
	}
	more := 0
	if len(parts) > 1 {
		more = 1
	}
	head := []byte(fmt.Sprintf("\x1b_G%s,m=%d;", prefix, more))
	head = append(head, first...)
	head = append(head, '\x1b', '\\')
	output = append(output, w.graphics(head)...)
	for index := 1; index < len(parts); index++ {
		moreValue := 0
		if index < len(parts)-1 {
			moreValue = 1
		}
		command := []byte(fmt.Sprintf("\x1b_Gm=%d,q=1;", moreValue))
		command = append(command, parts[index]...)
		command = append(command, '\x1b', '\\')
		output = append(output, w.graphics(command)...)
	}
	return output
}

// encodeBase64 returns the base64 text of data in a pooled buffer,
// avoiding the string-to-[]byte copy of EncodeToString.
func encodeBase64(data []byte) []byte {
	encoded := getBuffer(base64.StdEncoding.EncodedLen(len(data)))
	encoded = encoded[:base64.StdEncoding.EncodedLen(len(data))]
	base64.StdEncoding.Encode(encoded, data)
	return encoded
}

// placeRGB deletes the tile's previous placement and transmits its pixels as
// one paletted PNG placed by cell span.
func (w *Writer) placeRGB(tile Tile) []byte {
	// Paletted PNG is substantially smaller than lossless RGB/zlib for a
	// desktop while remaining sharp enough for text and UI chrome. Smaller
	// updates matter more than a few milliseconds of local encoding once the
	// byte stream passes through an SSH PTY and terminal parser.
	pngData, release, err := palettePNGPooled(tile.RGB, tile.Width, tile.Height)
	if err != nil {
		return nil
	}
	defer release()
	encoded := encodeBase64(pngData)
	defer putBuffer(encoded)
	output := w.graphics([]byte(fmt.Sprintf(
		"\x1b_Ga=d,d=i,i=%d,p=%d,q=1\x1b\\", tile.ImageID, PlacementID)))
	output = append(output, []byte(fmt.Sprintf("%s%d;%dH", csi, tile.Row+1, tile.Column+1))...)
	prefix := fmt.Sprintf("a=T,q=1,C=1,z=-1,f=100,i=%d,p=%d,c=%d,r=%d",
		tile.ImageID, PlacementID, tile.CellColumns, tile.CellRows)
	return w.appendTransmission(output, prefix, encoded)
}

// placeFull transmits one canvas image instead of making the terminal decode
// every tile.
func (w *Writer) placeFull(frame *RenderedFrame, imageID int) []byte {
	rgb := frame.RGB
	width, height := frame.contentSize()
	if rgb == nil {
		rgb = rgbPixels(frame.Image)
	}
	pngData, release, err := palettePNGPooled(rgb, width, height)
	if err != nil {
		return nil
	}
	defer release()
	encoded := encodeBase64(pngData)
	defer putBuffer(encoded)
	output := []byte(fmt.Sprintf("%s%d;%dH", csi, frame.Viewport.Y+1, frame.Viewport.X+1))
	prefix := fmt.Sprintf("a=T,q=1,C=1,z=-2,f=100,i=%d,p=%d,c=%d,r=%d",
		imageID, PlacementID, frame.Viewport.Width, frame.Viewport.Height)
	return w.appendTransmission(output, prefix, encoded)
}

// rgbPixels flattens the content image into packed RGB24.
func rgbPixels(img *image.RGBA) []byte {
	width := img.Rect.Dx()
	height := img.Rect.Dy()
	rgb := make([]byte, 0, width*height*3)
	for y := 0; y < height; y++ {
		base := img.PixOffset(0, y)
		for x := 0; x < width; x++ {
			offset := base + x*4
			rgb = append(rgb, img.Pix[offset], img.Pix[offset+1], img.Pix[offset+2])
		}
	}
	return rgb
}

// replaceFull atomically swaps the desktop canvas and discards obsolete
// delta tiles.
func (w *Writer) replaceFull(frame *RenderedFrame) []byte {
	imageID := fullImageID0
	if w.baseImageID == imageID {
		imageID = fullImageID1
	}
	var output []byte
	if w.probe.SynchronizedOutput {
		output = append(output, []byte(csi+"?2026h")...)
	}
	if w.baseImageID == 0 {
		output = append(output, w.ResetCanvas()...)
	}
	output = append(output, w.placeFull(frame, imageID)...)
	output = append(output, w.graphics([]byte("\x1b_Ga=d,d=Z,z=-1,q=1\x1b\\"))...)
	if w.baseImageID != 0 {
		output = append(output, w.graphics([]byte(fmt.Sprintf(
			"\x1b_Ga=d,d=I,i=%d,q=1\x1b\\", w.baseImageID)))...)
	}
	w.baseImageID = imageID
	if w.probe.SynchronizedOutput {
		output = append(output, []byte(csi+"?2026l")...)
	}
	return output
}

func (w *Writer) frame(tiles []Tile, clear bool) []byte {
	var output []byte
	if w.probe.SynchronizedOutput {
		output = append(output, []byte(csi+"?2026h")...)
	}
	if clear {
		w.cursorLoaded = false
		output = append(output, w.graphics([]byte("\x1b_Ga=d,d=A,q=1\x1b\\"))...)
		output = append(output, []byte(csi+"2J"+csi+"H")...)
	}
	if len(tiles) >= 4 {
		packets := make([][]byte, len(tiles))
		workers := min(4, max(1, runtime.NumCPU()))
		tasks := make(chan int)
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for index := range tasks {
					packets[index] = w.placeRGB(tiles[index])
					putBuffer(tiles[index].RGB)
				}
			}()
		}
		for index := range tiles {
			tasks <- index
		}
		close(tasks)
		wg.Wait()
		for _, packet := range packets {
			output = append(output, packet...)
		}
	} else {
		for _, tile := range tiles {
			output = append(output, w.placeRGB(tile)...)
			putBuffer(tile.RGB)
		}
	}
	if w.probe.SynchronizedOutput {
		output = append(output, []byte(csi+"?2026l")...)
	}
	return output
}

// Full encodes the whole frame as one canvas replacement.
func (w *Writer) Full(frame *RenderedFrame) []byte {
	return w.replaceFull(frame)
}

// Delta encodes changed tiles, escalating to a canvas replacement when the
// changed share crosses the threshold.
func (w *Writer) Delta(update FrameUpdate) []byte {
	if update.ChangedPercentage() >= FullReplaceThreshold {
		return w.replaceFull(update.Frame)
	}
	return w.frame(update.Changes, false)
}

// Update encodes a frame update; UNCHANGED produces no bytes.
func (w *Writer) Update(update FrameUpdate) []byte {
	switch update.Kind {
	case render.UpdateFull:
		return w.Full(update.Frame)
	case render.UpdateDelta:
		return w.Delta(update)
	default:
		return nil
	}
}

// cursorRGBA draws the 9x13 remote cursor sprite.
func cursorRGBA() (int, int, []byte) {
	width, height := 9, 13
	pixels := make([]byte, width*height*4)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			inside := x <= min(5, y/2) || (5 <= y && y <= 9 && 3 <= x && x <= 7)
			if !inside {
				continue
			}
			edge := x == 0 || y == 0 || x == min(5, y/2) || y == 5 || y == 9
			offset := (y*width + x) * 4
			if edge {
				pixels[offset+3] = 0xFF
			} else {
				pixels[offset] = 0xFF
				pixels[offset+1] = 0xFF
				pixels[offset+2] = 0xFF
				pixels[offset+3] = 0xFF
			}
		}
	}
	return width, height, pixels
}

func (w *Writer) inlineImage(
	imageID, column, row, xOffset, yOffset, width, height int,
	rgba []byte,
) []byte {
	encoded := encodeBase64(rgba)
	defer putBuffer(encoded)
	output := w.graphics([]byte(fmt.Sprintf(
		"\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", imageID, PlacementID)))
	output = append(output, []byte(fmt.Sprintf("%s%d;%dH", csi, row+1, column+1))...)
	prefix := fmt.Sprintf("a=T,q=1,C=1,z=1,f=32,i=%d,p=%d,s=%d,v=%d,X=%d,Y=%d",
		imageID, PlacementID, width, height, xOffset, yOffset)
	return w.appendTransmission(output, prefix, encoded)
}

// Cursor draws or hides the remote cursor position.
func (w *Writer) Cursor(frame *RenderedFrame, remoteX, remoteY int, visible bool) []byte {
	if !visible {
		return w.graphics([]byte(fmt.Sprintf(
			"\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", CursorImageID, PlacementID)))
	}
	viewport := frame.PixelViewport
	px := viewport.X + min(viewport.Width-1, max(0, remoteX*viewport.Width/viewport.DesktopWidth))
	py := viewport.Y + min(viewport.Height-1, max(0, remoteY*viewport.Height/viewport.DesktopHeight))
	column := px / frame.CellWidth
	xOffset := px % frame.CellWidth
	row := py / frame.CellHeight
	yOffset := py % frame.CellHeight
	if !w.cursorLoaded {
		w.cursorLoaded = true
		width, height, rgba := cursorRGBA()
		return w.inlineImage(CursorImageID, column, row, xOffset, yOffset, width, height, rgba)
	}
	output := w.graphics([]byte(fmt.Sprintf(
		"\x1b_Ga=d,d=i,i=%d,p=%d,q=2\x1b\\", CursorImageID, PlacementID)))
	output = append(output, []byte(fmt.Sprintf("%s%d;%dH", csi, row+1, column+1))...)
	output = append(output, w.graphics([]byte(fmt.Sprintf(
		"\x1b_Ga=p,q=2,C=1,z=1,i=%d,p=%d,X=%d,Y=%d\x1b\\",
		CursorImageID, PlacementID, xOffset, yOffset)))...)
	return output
}
