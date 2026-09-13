package render

import "testing"

func TestCapabilityDetection(t *testing.T) {
	cases := []struct {
		env   map[string]string
		color ColorMode
	}{
		{map[string]string{"TERM": "xterm-256color"}, Color256},
		{map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, ColorTruecolor},
		{map[string]string{"TERM": "vt100"}, Color16},
		{map[string]string{"TERM": "xterm-256color", "SSHDESK_COLOR": "auto"}, Color256},
		{map[string]string{"TERM": "xterm-kitty"}, ColorTruecolor},
		{map[string]string{"TERM": "xterm-256color", "SSHDESK_COLOR": "16"}, Color16},
		{map[string]string{"TERM": "xterm-256color", "SSHDESK_COLOR": "truecolor"}, ColorTruecolor},
	}
	for _, tc := range cases {
		capabilities, err := DetectCapabilities(tc.env)
		if err != nil {
			t.Fatalf("env %v: %v", tc.env, err)
		}
		if capabilities.Color != tc.color {
			t.Errorf("env %v: color = %v, want %v", tc.env, capabilities.Color, tc.color)
		}
	}
}

func TestCapabilityDetectionRejectsDumbTerminals(t *testing.T) {
	for _, term := range []string{"", "dumb", "unknown"} {
		if _, err := DetectCapabilities(map[string]string{"TERM": term}); err == nil {
			t.Errorf("TERM %q must fail", term)
		}
	}
}

func TestCapabilityDetectionRejectsBadOverrides(t *testing.T) {
	for _, env := range []map[string]string{
		{"TERM": "xterm", "SSHDESK_COLOR": "millions"},
		{"TERM": "xterm", "SSHDESK_MOUSE": "maybe"},
		{"TERM": "xterm", "SSHDESK_UNICODE": "sometimes"},
	} {
		if _, err := DetectCapabilities(env); err == nil {
			t.Errorf("env %v must fail", env)
		}
	}
}

func TestMouseAndUnicodeInference(t *testing.T) {
	sgr, err := DetectCapabilities(map[string]string{"TERM": "xterm-256color"})
	if err != nil {
		t.Fatal(err)
	}
	if !sgr.Mouse || !sgr.SGRMouse || !sgr.Unicode {
		t.Errorf("xterm-256color: %+v", sgr)
	}
	plain, err := DetectCapabilities(map[string]string{"TERM": "vt100"})
	if err != nil {
		t.Fatal(err)
	}
	if !plain.Mouse || plain.SGRMouse || plain.Unicode {
		t.Errorf("vt100: %+v", plain)
	}
	disabled, err := DetectCapabilities(map[string]string{
		"TERM":            "xterm-256color",
		"SSHDESK_MOUSE":   "no",
		"SSHDESK_UNICODE": "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if disabled.Mouse || disabled.SGRMouse || disabled.Unicode {
		t.Errorf("overrides: %+v", disabled)
	}
	linux, err := DetectCapabilities(map[string]string{"TERM": "linux"})
	if err != nil {
		t.Fatal(err)
	}
	if linux.SGRMouse || !linux.Unicode {
		t.Errorf("linux console: %+v", linux)
	}
}

func TestParseMaxFPS(t *testing.T) {
	if fps, _ := ParseMaxFPS("auto", true); fps != 60.0 {
		t.Errorf("sharp auto = %v", fps)
	}
	if fps, _ := ParseMaxFPS("", false); fps != 30.0 {
		t.Errorf("ansi auto = %v", fps)
	}
	if fps, _ := ParseMaxFPS("90", true); fps != 90.0 {
		t.Errorf("90 = %v", fps)
	}
	if _, err := ParseMaxFPS("240", true); err == nil {
		t.Error("240 must fail")
	}
	if _, err := ParseMaxFPS("fast", true); err == nil {
		t.Error("non-numeric must fail")
	}
}

func TestParseRenderScale(t *testing.T) {
	if scale, _ := ParseRenderScale("auto"); scale != 1.0 {
		t.Errorf("auto = %v", scale)
	}
	if scale, _ := ParseRenderScale("0.75"); scale != 0.75 {
		t.Errorf("0.75 = %v", scale)
	}
	if _, err := ParseRenderScale("0.1"); err == nil {
		t.Error("0.1 must fail")
	}
	if !IsAutoRenderScale("auto") || !IsAutoRenderScale("") || IsAutoRenderScale("0.5") {
		t.Error("IsAutoRenderScale mismatch")
	}
}
