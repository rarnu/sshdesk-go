package render

import (
	"errors"
	"strconv"
	"strings"
)

type ColorMode int

const (
	ColorTruecolor ColorMode = iota
	Color256
	Color16
)

func (m ColorMode) String() string {
	switch m {
	case ColorTruecolor:
		return "truecolor"
	case Color256:
		return "256"
	default:
		return "16"
	}
}

// Capabilities describes terminal features inferred from standard
// SSH-propagated environment values.
type Capabilities struct {
	Term     string
	Color    ColorMode
	Mouse    bool
	SGRMouse bool
	Unicode  bool
}

var ErrNoTerminal = errors.New("SSHDESK requires an interactive ANSI terminal; TERM is missing or dumb")

func containsAny(value string, names ...string) bool {
	for _, name := range names {
		if strings.Contains(value, name) {
			return true
		}
	}
	return false
}

// DetectCapabilities infers terminal features from environment variables
// without sending any queries.
func DetectCapabilities(env map[string]string) (Capabilities, error) {
	term := strings.ToLower(strings.TrimSpace(env["TERM"]))
	if term == "" || term == "dumb" || term == "unknown" {
		return Capabilities{}, ErrNoTerminal
	}

	override := strings.ToLower(strings.TrimSpace(env["SSHDESK_COLOR"]))
	colorterm := strings.ToLower(strings.TrimSpace(env["COLORTERM"]))
	if override == "auto" {
		override = ""
	}
	var color ColorMode
	switch override {
	case "truecolor", "24bit", "24-bit":
		color = ColorTruecolor
	case "256", "256color":
		color = Color256
	case "16", "ansi", "basic":
		color = Color16
	case "":
		if colorterm == "truecolor" || colorterm == "24bit" ||
			containsAny(term, "direct", "kitty", "wezterm", "alacritty") {
			color = ColorTruecolor
		} else if strings.Contains(term, "256color") {
			color = Color256
		} else {
			color = Color16
		}
	default:
		return Capabilities{}, errors.New("SSHDESK_COLOR must be truecolor, 256, or 16")
	}

	mouseOverride := strings.ToLower(strings.TrimSpace(env["SSHDESK_MOUSE"]))
	if mouseOverride == "" {
		mouseOverride = "auto"
	}
	switch mouseOverride {
	case "auto", "0", "1", "false", "true", "no", "yes":
	default:
		return Capabilities{}, errors.New("SSHDESK_MOUSE must be auto, 0/1, false/true, or no/yes")
	}
	mouse := mouseOverride != "0" && mouseOverride != "false" && mouseOverride != "no"
	sgrMouse := mouse && containsAny(term, "xterm", "screen", "tmux", "rxvt", "kitty", "wezterm", "alacritty")

	unicodeOverride := strings.ToLower(strings.TrimSpace(env["SSHDESK_UNICODE"]))
	if unicodeOverride == "" {
		unicodeOverride = "auto"
	}
	switch unicodeOverride {
	case "auto", "0", "1", "false", "true", "no", "yes":
	default:
		return Capabilities{}, errors.New("SSHDESK_UNICODE must be auto, 0/1, false/true, or no/yes")
	}
	var unicodeSupport bool
	switch unicodeOverride {
	case "0", "false", "no":
		unicodeSupport = false
	case "1", "true", "yes":
		unicodeSupport = true
	default:
		unicodeSupport = containsAny(term, "xterm", "screen", "tmux", "rxvt", "kitty", "wezterm", "alacritty", "linux")
	}
	return Capabilities{
		Term:     term,
		Color:    color,
		Mouse:    mouse,
		SGRMouse: sgrMouse,
		Unicode:  unicodeSupport,
	}, nil
}

var errMaxFPSRange = errors.New("SSHDESK_MAX_FPS must be between 1 and 120")

// ParseMaxFPS validates an active refresh limit; sharp selects the 60 FPS
// pixel-renderer default, otherwise the ANSI default is 30.
func ParseMaxFPS(value string, sharp bool) (float64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" || trimmed == "auto" {
		if sharp {
			return 60.0, nil
		}
		return 30.0, nil
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, errors.New("SSHDESK_MAX_FPS must be auto or a number")
	}
	if parsed < 1.0 || parsed > 120.0 {
		return 0, errMaxFPSRange
	}
	return parsed, nil
}

var errScaleRange = errors.New("SSHDESK_SCALE must be between 0.25 and 1.0")

// ParseRenderScale validates a render scale value.
func ParseRenderScale(value string) (float64, error) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" || trimmed == "auto" {
		return 1.0, nil
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil {
		return 0, errors.New("SSHDESK_SCALE must be auto or a number")
	}
	if parsed < 0.25 || parsed > 1.0 {
		return 0, errScaleRange
	}
	return parsed, nil
}

// IsAutoRenderScale reports whether the render scale follows the automatic
// backpressure-driven adjustment.
func IsAutoRenderScale(value string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	return trimmed == "" || trimmed == "auto"
}
