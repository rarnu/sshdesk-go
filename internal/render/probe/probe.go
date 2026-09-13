// Package probe detects Kitty graphics support and terminal pixel geometry
// by querying the terminal through the SSH PTY before the session starts.
package probe

import (
	"bytes"
	"regexp"
	"strconv"
)

const (
	// ProbeImageID is the Kitty graphics id used by the capability query.
	ProbeImageID = 0x765
	// ProbeTimeout is the default reply wait in seconds.
	ProbeTimeout = 0.75
)

var (
	kittyReplyRE = regexp.MustCompile(`\x1b_G([^\x1b]*?)\x1b\\`)
	textAreaRE   = regexp.MustCompile(`\x1b\[4;(\d{1,6});(\d{1,6})t`)
	cellSizeRE   = regexp.MustCompile(`\x1b\[6;(\d{1,4});(\d{1,4})t`)
	da1RE        = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)
	pixelMouseRE = regexp.MustCompile(`\x1b\[\?1016;[123]\$y`)
	syncOutRE    = regexp.MustCompile(`\x1b\[\?2026;[123]\$y`)
)

// TmuxPassthrough wraps an escape sequence for tmux's DCS passthrough.
func TmuxPassthrough(sequence []byte) []byte {
	wrapped := make([]byte, 0, len(sequence)*2+12)
	wrapped = append(wrapped, '\x1b', 'P', 't', 'm', 'u', 'x', ';')
	for _, b := range sequence {
		if b == '\x1b' {
			wrapped = append(wrapped, '\x1b')
		}
		wrapped = append(wrapped, b)
	}
	return append(wrapped, '\x1b', '\\')
}

// GraphicsProbe reports the terminal features the Kitty renderer needs.
type GraphicsProbe struct {
	KittyGraphics      bool
	PixelMouse         bool
	SynchronizedOutput bool
	TextWidth          int
	TextHeight         int
	CellWidth          int
	CellHeight         int
}

// Usable reports whether Kitty graphics and the full pixel geometry exist.
func (p GraphicsProbe) Usable() bool {
	return p.KittyGraphics &&
		p.TextWidth > 0 &&
		p.TextHeight > 0 &&
		p.CellWidth > 0 &&
		p.CellHeight > 0
}

func bounded(value []byte, maximum int) int {
	parsed, err := strconv.Atoi(string(value))
	if err != nil || parsed <= 0 || parsed > maximum {
		return 0
	}
	return parsed
}

// ParseGraphicsProbe parses terminal replies without trusting their
// dimensions or payload sizes. ioctlPixels carries the TIOCGWINSZ pixel
// size as (width, height); pass [2]int{} when unavailable.
func ParseGraphicsProbe(reply []byte, columns, rows int, ioctlPixels [2]int) GraphicsProbe {
	kitty := false
	needle := []byte("i=" + strconv.Itoa(ProbeImageID))
	limit := min(len(reply), 16384)
	for _, match := range kittyReplyRE.FindAllSubmatch(reply[:limit], -1) {
		payload := match[1]
		if bytes.Contains(payload, needle) &&
			(bytes.Contains(payload, []byte(";OK")) || bytes.Contains(payload, []byte("=OK"))) {
			kitty = true
			break
		}
	}

	textWidth, textHeight := ioctlPixels[0], ioctlPixels[1]
	if (textWidth <= 0 || textHeight <= 0) && textAreaRE.Match(reply) {
		match := textAreaRE.FindSubmatch(reply)
		height, width := match[1], match[2]
		textWidth = bounded(width, 65535)
		textHeight = bounded(height, 65535)
	}
	if !(textWidth > 0 && textWidth <= 65535 && textHeight > 0 && textHeight <= 65535) {
		textWidth, textHeight = 0, 0
	}

	cellWidth, cellHeight := 0, 0
	if match := cellSizeRE.FindSubmatch(reply); match != nil {
		height, width := match[1], match[2]
		cellWidth = bounded(width, 1024)
		cellHeight = bounded(height, 1024)
	}
	if cellWidth == 0 && textWidth != 0 && columns != 0 {
		cellWidth = max(1, textWidth/columns)
	}
	if cellHeight == 0 && textHeight != 0 && rows != 0 {
		cellHeight = max(1, textHeight/rows)
	}

	return GraphicsProbe{
		KittyGraphics:      kitty,
		PixelMouse:         pixelMouseRE.Match(reply),
		SynchronizedOutput: syncOutRE.Match(reply),
		TextWidth:          textWidth,
		TextHeight:         textHeight,
		CellWidth:          cellWidth,
		CellHeight:         cellHeight,
	}
}
