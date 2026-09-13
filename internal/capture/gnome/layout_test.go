package gnome

import (
	"testing"
)

// twoMonitorState mirrors the Python test fixture: a 1920x1080 monitor at
// the origin and a 2560x1600 monitor at x=1920 with 2.0 fractional scale.
func twoMonitorState(layoutMode int) DisplayState {
	monitorA := MonitorSpec{Connector: "DP-1", Vendor: "vendor", Product: "left", Serial: "1"}
	monitorB := MonitorSpec{Connector: "eDP-1", Vendor: "vendor", Product: "right", Serial: "2"}
	return DisplayState{
		Monitors: []Monitor{
			{Spec: monitorA, Modes: []MonitorMode{{ID: "mode-a", Width: 1920, Height: 1080, IsCurrent: true}}},
			{Spec: monitorB, Modes: []MonitorMode{{ID: "mode-b", Width: 2560, Height: 1600, IsCurrent: true}}},
		},
		Logical: []LogicalMonitor{
			{X: 0, Y: 0, Scale: 1.0, Transform: 0, Primary: true, Monitors: []MonitorSpec{monitorA}},
			{X: 1920, Y: 0, Scale: 2.0, Transform: 0, Primary: false, Monitors: []MonitorSpec{monitorB}},
		},
		LayoutMode: layoutMode,
	}
}

func TestDesktopAreaFractionalScaling(t *testing.T) {
	area, err := DesktopArea(twoMonitorState(1))
	if err != nil {
		t.Fatalf("DesktopArea() error = %v", err)
	}
	if area != (Rectangle{X: 0, Y: 0, Width: 3200, Height: 1080}) {
		t.Fatalf("DesktopArea() = %+v, want (0,0,3200,1080)", area)
	}
}

func TestDesktopAreaPhysicalLayoutIgnoresScale(t *testing.T) {
	area, err := DesktopArea(twoMonitorState(2))
	if err != nil {
		t.Fatalf("DesktopArea() error = %v", err)
	}
	if area != (Rectangle{X: 0, Y: 0, Width: 4480, Height: 1600}) {
		t.Fatalf("DesktopArea() = %+v, want (0,0,4480,1600)", area)
	}
}

func TestDesktopAreaOddTransformSwapsDimensions(t *testing.T) {
	state := twoMonitorState(2)
	state.Logical = state.Logical[:1]
	state.Logical[0].Transform = 1
	area, err := DesktopArea(state)
	if err != nil {
		t.Fatalf("DesktopArea() error = %v", err)
	}
	if area.Width != 1080 || area.Height != 1920 {
		t.Fatalf("DesktopArea() = %+v, want a 1080x1920 rotated monitor", area)
	}
}

func TestDesktopAreaSkipsMonitorsWithoutCurrentMode(t *testing.T) {
	state := twoMonitorState(2)
	state.Monitors[1].Modes[0].IsCurrent = false
	area, err := DesktopArea(state)
	if err != nil {
		t.Fatalf("DesktopArea() error = %v", err)
	}
	if area != (Rectangle{X: 0, Y: 0, Width: 1920, Height: 1080}) {
		t.Fatalf("DesktopArea() = %+v, want only the first monitor", area)
	}
}

func TestDesktopAreaNegativeOffsets(t *testing.T) {
	state := twoMonitorState(2)
	state.Logical[0].X = -1920
	area, err := DesktopArea(state)
	if err != nil {
		t.Fatalf("DesktopArea() error = %v", err)
	}
	if area.X != -1920 || area.Width != 6400 {
		t.Fatalf("DesktopArea() = %+v, want origin -1920 width 6400", area)
	}
}

func TestDesktopAreaNoActiveMonitors(t *testing.T) {
	state := twoMonitorState(2)
	state.Monitors[0].Modes[0].IsCurrent = false
	state.Monitors[1].Modes[0].IsCurrent = false
	_, err := DesktopArea(state)
	if err == nil || err.Error() != "GNOME reported no active monitors" {
		t.Fatalf("DesktopArea() error = %v", err)
	}
}

func TestDesktopAreaInvalidSize(t *testing.T) {
	spec := MonitorSpec{Connector: "DP-1"}
	state := DisplayState{
		Monitors: []Monitor{
			{Spec: spec, Modes: []MonitorMode{{ID: "mode", Width: 40000, Height: 1080, IsCurrent: true}}},
		},
		Logical: []LogicalMonitor{
			{X: 0, Y: 0, Scale: 1.0, Monitors: []MonitorSpec{spec}},
		},
		LayoutMode: 2,
	}
	_, err := DesktopArea(state)
	if err == nil || err.Error() != "GNOME reported an invalid desktop size: 40000x1080" {
		t.Fatalf("DesktopArea() error = %v", err)
	}
}
