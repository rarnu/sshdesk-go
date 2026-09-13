package probe

import "testing"

func TestTmuxPassthrough(t *testing.T) {
	got := TmuxPassthrough([]byte("\x1b_Gi=1;OK\x1b\\"))
	want := []byte("\x1bPtmux;\x1b\x1b_Gi=1;OK\x1b\x1b\\\x1b\\")
	if string(got) != string(want) {
		t.Errorf("tmux passthrough = %q, want %q", got, want)
	}
}

func TestParseGraphicsProbeAndBoundedPixelGeometry(t *testing.T) {
	reply := []byte(
		"\x1b_Gi=1893;OK\x1b\\" +
			"\x1b[4;480;800t\x1b[6;20;10t" +
			"\x1b[?1016;2$y\x1b[?2026;1$y\x1b[?62;c")
	probe := ParseGraphicsProbe(reply, 80, 24, [2]int{})
	if !probe.Usable() {
		t.Error("probe must be usable with graphics and full geometry")
	}
	if !probe.PixelMouse {
		t.Error("pixel mouse not detected")
	}
	if !probe.SynchronizedOutput {
		t.Error("synchronized output not detected")
	}
	if probe.CellWidth != 10 || probe.CellHeight != 20 {
		t.Errorf("cell geometry = %dx%d, want 10x20", probe.CellWidth, probe.CellHeight)
	}
	if probe.TextWidth != 800 || probe.TextHeight != 480 {
		t.Errorf("text geometry = %dx%d, want 800x480", probe.TextWidth, probe.TextHeight)
	}

	malicious := ParseGraphicsProbe(
		[]byte("\x1b_Gi=1893;OK\x1b\\\x1b[4;999999;999999t"), 80, 24, [2]int{})
	if malicious.Usable() {
		t.Error("oversized text geometry must not be trusted")
	}
}

func TestParseGraphicsProbeWithoutKittyReply(t *testing.T) {
	probe := ParseGraphicsProbe([]byte("\x1b[4;480;800t\x1b[6;20;10t\x1b[?62;c"), 80, 24, [2]int{})
	if probe.KittyGraphics || probe.Usable() {
		t.Error("missing Kitty reply must report no graphics")
	}
	if probe.PixelMouse || probe.SynchronizedOutput {
		t.Error("missing mode replies must report no optional features")
	}
}

func TestParseGraphicsProbeUsesIoctlPixels(t *testing.T) {
	probe := ParseGraphicsProbe([]byte("\x1b_Gi=1893;OK\x1b\\\x1b[6;20;10t"), 80, 24, [2]int{800, 480})
	if probe.TextWidth != 800 || probe.TextHeight != 480 {
		t.Errorf("ioctl geometry = %dx%d, want 800x480", probe.TextWidth, probe.TextHeight)
	}
	if !probe.Usable() {
		t.Error("ioctl geometry must satisfy usability")
	}
}

func TestParseGraphicsProbeDerivesCellGeometry(t *testing.T) {
	probe := ParseGraphicsProbe(
		[]byte("\x1b_Gi=1893;OK\x1b\\\x1b[4;480;800t"), 80, 24, [2]int{})
	if probe.CellWidth != 10 || probe.CellHeight != 20 {
		t.Errorf("derived cell geometry = %dx%d, want 10x20", probe.CellWidth, probe.CellHeight)
	}
	if !probe.Usable() {
		t.Error("derived cell geometry must satisfy usability")
	}
}

func TestParseGraphicsProbeIgnoresUnrelatedKittyReplies(t *testing.T) {
	probe := ParseGraphicsProbe([]byte("\x1b_Gi=9999;OK\x1b\\\x1b[4;480;800t\x1b[6;20;10t"), 80, 24, [2]int{})
	if probe.KittyGraphics {
		t.Error("reply for another image id must not count as graphics support")
	}
}

func TestGraphicsProbeUsable(t *testing.T) {
	if (GraphicsProbe{}).Usable() {
		t.Error("zero probe must not be usable")
	}
	probe := GraphicsProbe{
		KittyGraphics: true,
		TextWidth:     800,
		TextHeight:    480,
		CellWidth:     10,
		CellHeight:    20,
	}
	if !probe.Usable() {
		t.Error("complete probe must be usable")
	}
}
