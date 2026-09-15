package ansi

import (
	"fmt"
	"strings"

	"github.com/rarnu/sshdesk-go/internal/render"
)

func commas(value float64) string {
	formatted := fmt.Sprintf("%.0f", value)
	negative := strings.HasPrefix(formatted, "-")
	if negative {
		formatted = formatted[1:]
	}
	var groups []string
	for len(formatted) > 3 {
		groups = append([]string{formatted[len(formatted)-3:]}, groups...)
		formatted = formatted[:len(formatted)-3]
	}
	groups = append([]string{formatted}, groups...)
	joined := strings.Join(groups, ",")
	if negative {
		return "-" + joined
	}
	return joined
}

func truncateRunes(text string, width int) string {
	runes := []rune(text)
	if len(runes) > width {
		return string(runes[:width])
	}
	return text
}

// Overlay draws the four-line statistics block starting on the second row.
func (w *Writer) Overlay(stats render.StatsSnapshot) []byte {
	lines := []string{
		fmt.Sprintf(" SSHDESK  %4.1f updates  %4.1f capture FPS  %4.1f%% changed ",
			stats.FPS, stats.CapturedFPS, stats.ChangedPercentage),
		fmt.Sprintf(" cap %4.1f  render %4.1f  diff %4.1f  enc %4.1f  write %4.1f  age %4.1f ms ",
			stats.CaptureMs, stats.RenderMs, stats.DiffMs, stats.EncodeMs, stats.WriteMs, stats.FrameAgeMs),
		fmt.Sprintf(" tx %s  rx %s Kbit/s  total %s/%s KiB  RTT %4.1f ms ",
			commas(stats.BytesSentPerSecond*8/1000),
			commas(stats.BytesReceivedPerSec*8/1000),
			commas(float64(stats.BytesSent)/1024),
			commas(float64(stats.BytesReceived)/1024),
			stats.LatencyMs),
		fmt.Sprintf(" term %dx%d  remote %dx%d  full %d  delta %d  drop %d ",
			stats.TerminalWidth, stats.TerminalHeight,
			stats.RemoteWidth, stats.RemoteHeight,
			stats.FullFrames, stats.DeltaFrames, stats.DroppedFrames),
	}
	var output strings.Builder
	width := stats.TerminalWidth
	if width == 0 {
		width = 80
	}
	height := stats.TerminalHeight
	if height == 0 {
		height = 24
	}
	_, overlayEscape, _ := w.cellStyle(render.RGB{R: 255, G: 255, B: 255}, render.RGB{R: 22, G: 28, B: 38})
	limit := max(0, height-1)
	if limit > len(lines) {
		limit = len(lines)
	}
	for index := 0; index < limit; index++ {
		fmt.Fprintf(&output, "%s%d;1H%s%s", csi, index+2, overlayEscape, truncateRunes(lines[index], width))
	}
	output.WriteString(csi + "0m")
	return []byte(output.String())
}
