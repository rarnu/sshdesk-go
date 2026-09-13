// Package gnome implements the persistent GNOME Shell capture through
// Mutter's D-Bus RemoteDesktop/ScreenCast API and a gst-launch PipeWire
// stream. The display-layout resolution is platform-neutral pure logic;
// the D-Bus transport (godbus, pure Go) and the gst-launch child process
// compile everywhere, so the whole package is testable without a GNOME
// session.
package gnome

import (
	"errors"
	"fmt"
	"math"
)

// D-Bus names, mirroring the Python class constants.
const (
	ScreencastName             = "org.gnome.Mutter.ScreenCast"
	ScreencastRoot             = "/org/gnome/Mutter/ScreenCast"
	ScreencastRootInterface    = "org.gnome.Mutter.ScreenCast"
	ScreencastSessionInterface = "org.gnome.Mutter.ScreenCast.Session"
	ScreencastStreamInterface  = "org.gnome.Mutter.ScreenCast.Stream"
	RemoteName                 = "org.gnome.Mutter.RemoteDesktop"
	RemoteRoot                 = "/org/gnome/Mutter/RemoteDesktop"
	RemoteRootInterface        = "org.gnome.Mutter.RemoteDesktop"
	RemoteSessionInterface     = "org.gnome.Mutter.RemoteDesktop.Session"
	DisplayName                = "org.gnome.Mutter.DisplayConfig"
	DisplayRoot                = "/org/gnome/Mutter/DisplayConfig"
	DisplayInterface           = "org.gnome.Mutter.DisplayConfig"
	PropertiesInterface        = "org.freedesktop.DBus.Properties"
)

// MonitorSpec is one Mutter monitor specification (connector, vendor,
// product, serial).
type MonitorSpec struct {
	Connector string
	Vendor    string
	Product   string
	Serial    string
}

// MonitorMode is one video mode of a physical monitor.
type MonitorMode struct {
	ID        string
	Width     int
	Height    int
	IsCurrent bool
}

// Monitor is one physical monitor with its modes.
type Monitor struct {
	Spec  MonitorSpec
	Modes []MonitorMode
}

// LogicalMonitor is one entry of Mutter's logical monitor list.
type LogicalMonitor struct {
	X         int
	Y         int
	Scale     float64
	Transform int
	Primary   bool
	Monitors  []MonitorSpec
}

// DisplayState is the parsed Mutter GetCurrentState reply.
type DisplayState struct {
	Monitors   []Monitor
	Logical    []LogicalMonitor
	LayoutMode int
}

// Rectangle is one desktop area in pixels.
type Rectangle struct {
	X      int
	Y      int
	Width  int
	Height int
}

// DesktopArea computes Mutter's complete logical desktop rectangle,
// mirroring the Python _desktop_area: odd transforms swap the mode
// dimensions, layout-mode 1 divides by the fractional scale, and the
// desktop is the bounding box of all logical monitors.
func DesktopArea(state DisplayState) (Rectangle, error) {
	currentModes := make(map[MonitorSpec][2]int)
	for _, monitor := range state.Monitors {
		for _, mode := range monitor.Modes {
			if mode.IsCurrent {
				currentModes[monitor.Spec] = [2]int{mode.Width, mode.Height}
				break
			}
		}
	}

	var rectangles []Rectangle
	for _, logical := range state.Logical {
		width, height := 0, 0
		for _, spec := range logical.Monitors {
			size, ok := currentModes[spec]
			if !ok {
				continue
			}
			width = max(width, size[0])
			height = max(height, size[1])
		}
		if width == 0 && height == 0 {
			continue
		}
		if logical.Transform%2 != 0 {
			width, height = height, width
		}
		if state.LayoutMode == 1 {
			scale := math.Max(logical.Scale, 0.01)
			width = max(1, int(math.RoundToEven(float64(width)/scale)))
			height = max(1, int(math.RoundToEven(float64(height)/scale)))
		}
		rectangles = append(rectangles, Rectangle{X: logical.X, Y: logical.Y, Width: width, Height: height})
	}

	if len(rectangles) == 0 {
		return Rectangle{}, errors.New("GNOME reported no active monitors")
	}
	left, top := rectangles[0].X, rectangles[0].Y
	right, bottom := left, top
	for _, rectangle := range rectangles {
		left = min(left, rectangle.X)
		top = min(top, rectangle.Y)
		right = max(right, rectangle.X+rectangle.Width)
		bottom = max(bottom, rectangle.Y+rectangle.Height)
	}
	width, height := right-left, bottom-top
	if width < 1 || width > 32768 || height < 1 || height > 32768 {
		return Rectangle{}, fmt.Errorf("GNOME reported an invalid desktop size: %dx%d", width, height)
	}
	return Rectangle{X: left, Y: top, Width: width, Height: height}, nil
}
