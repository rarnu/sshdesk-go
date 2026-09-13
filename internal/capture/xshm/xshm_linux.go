//go:build linux

package xshm

import (
	"fmt"
	"image"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/shm"
	"github.com/jezek/xgb/xproto"
	"golang.org/x/sys/unix"

	"github.com/rylena/sshdesk-go/internal/capture"
)

const (
	zpixmap   = 2
	allPlanes = 0xFFFFFFFF
)

// Frame is one captured image with timing and digest.
type Frame struct {
	Image         *image.RGBA
	CapturedNs    int64
	ContentDigest []byte
}

// Capture is the on-demand MIT-SHM capture, mirroring the Python XShmCapture.
type Capture struct {
	conn             *xgb.Conn
	root             xproto.Drawable
	Width            int
	Height           int
	bytesPerLine     int
	seg              shm.Seg
	shmid            int
	segment          []byte
	attached         bool
	markedForRemoval bool
}

// New opens the display, validates the pixel layout, and attaches one shared
// segment (marked IPC_RMID immediately, like the Python version).
func New(displayName string, width, height int) (*Capture, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("X11 desktop dimensions must be positive")
	}
	conn, err := xgb.NewConnDisplay(displayName)
	if err != nil {
		return nil, fmt.Errorf("cannot open X11 display %q for shared capture", displayName)
	}
	c := &Capture{conn: conn, Width: width, Height: height, shmid: -1}
	if err := c.open(); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}

func (c *Capture) open() error {
	if err := shm.Init(c.conn); err != nil {
		return fmt.Errorf("the X11 MIT-SHM extension is unavailable")
	}
	setup := xproto.Setup(c.conn)
	screen := setup.DefaultScreen(c.conn)
	c.root = xproto.Drawable(screen.Root)
	layout, err := RootLayout(setup, screen)
	if err != nil {
		return err
	}
	if err := layout.Validate(); err != nil {
		return err
	}
	c.bytesPerLine = c.Width * 4
	size := c.bytesPerLine * c.Height

	shmid, err := unix.SysvShmGet(unix.IPC_PRIVATE, size, unix.IPC_CREAT|0o600)
	if err != nil {
		return fmt.Errorf("shmget failed for X11 capture: %v", err)
	}
	c.shmid = shmid
	segment, err := unix.SysvShmAttach(shmid, 0, 0)
	if err != nil {
		return fmt.Errorf("shmat failed for X11 capture: %v", err)
	}
	c.segment = segment

	seg, err := shm.NewSegId(c.conn)
	if err != nil {
		return err
	}
	c.seg = seg
	if err := shm.AttachChecked(c.conn, seg, uint32(c.shmid), false).Check(); err != nil {
		return fmt.Errorf("XShmAttach failed: %v", err)
	}
	c.attached = true
	c.conn.Sync()
	if _, err := unix.SysvShmCtl(shmid, unix.IPC_RMID, nil); err == nil {
		c.markedForRemoval = true
	}
	return nil
}

// RootLayout resolves the bpp and channel masks of the root visual.
func RootLayout(setup *xproto.SetupInfo, screen *xproto.ScreenInfo) (PixelLayout, error) {
	layout := PixelLayout{}
	for _, depth := range screen.AllowedDepths {
		for _, visual := range depth.Visuals {
			if visual.VisualId == screen.RootVisual {
				layout.RedMask = visual.RedMask
				layout.GreenMask = visual.GreenMask
				layout.BlueMask = visual.BlueMask
			}
		}
		if depth.Depth == screen.RootDepth {
			for _, format := range setup.PixmapFormats {
				if format.Depth == depth.Depth {
					layout.BitsPerPixel = int(format.BitsPerPixel)
				}
			}
		}
	}
	return layout, nil
}

// Capture reads one desktop image and scales it to the target size.
func (c *Capture) Capture(targetWidth, targetHeight int) (Frame, error) {
	if c.conn == nil {
		return Frame{}, fmt.Errorf("X11 shared capture is closed")
	}
	if _, err := shm.GetImage(c.conn, c.root, 0, 0, uint16(c.Width), uint16(c.Height), allPlanes, zpixmap, c.seg, 0).Reply(); err != nil {
		return Frame{}, fmt.Errorf("XShmGetImage failed: %v", err)
	}
	capturedNs := time.Now().UnixNano()
	if targetWidth < 1 || targetWidth > c.Width || targetHeight < 1 || targetHeight > c.Height {
		return Frame{}, fmt.Errorf("X11 shared capture target is out of bounds")
	}
	img, err := BGRXToRGB(c.segment, c.Width, c.Height, c.bytesPerLine)
	if err != nil {
		return Frame{}, err
	}
	img = Scale(img, targetWidth, targetHeight)
	return Frame{Image: img, CapturedNs: capturedNs, ContentDigest: capture.DigestPixels(rgb24(img))}, nil
}

// rgb24 packs the image into RGB24 bytes for the content digest.
func rgb24(img *image.RGBA) []byte {
	width := img.Rect.Dx()
	height := img.Rect.Dy()
	out := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			s := img.PixOffset(x, y)
			d := (y*width + x) * 3
			out[d] = img.Pix[s]
			out[d+1] = img.Pix[s+1]
			out[d+2] = img.Pix[s+2]
		}
	}
	return out
}

// Close detaches the segment, releases the shared memory, and disconnects.
func (c *Capture) Close() {
	if c.conn != nil && c.attached {
		shm.Detach(c.conn, c.seg)
		c.conn.Sync()
		c.attached = false
	}
	if c.segment != nil {
		unix.SysvShmDetach(c.segment)
		c.segment = nil
	}
	if c.shmid >= 0 && !c.markedForRemoval {
		unix.SysvShmCtl(c.shmid, unix.IPC_RMID, nil)
	}
	c.shmid = -1
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}
