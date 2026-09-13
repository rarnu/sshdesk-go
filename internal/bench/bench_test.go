package bench

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rylena/sshdesk-go/internal/render"
)

func TestBenchOutputFormat(t *testing.T) {
	result, err := Run(0.2, 100, 30, render.Color256)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	Print(&output, result)
	lines := strings.Split(strings.TrimRight(output.String(), "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("report lines = %d, want 8:\n%s", len(lines), output.String())
	}
	prefixes := []string{
		"Session duration:       ",
		"Average FPS:            ",
		"Average bandwidth:      ",
		"Peak bandwidth:         ",
		"Input parse latency:    ",
		"Full frames:            ",
		"Delta frames:           ",
		"Average changed area:   ",
	}
	for index, prefix := range prefixes {
		if !strings.HasPrefix(lines[index], prefix) {
			t.Errorf("line %d = %q, want prefix %q", index, lines[index], prefix)
		}
	}
	if !strings.HasSuffix(lines[0], "s") || !strings.HasSuffix(lines[2], "Kbit/s") ||
		!strings.HasSuffix(lines[4], "ms") || !strings.HasSuffix(lines[7], "%") {
		t.Errorf("units missing:\n%s", output.String())
	}
	if result.FullFrames < 1 {
		t.Error("expected at least one full frame")
	}
	if result.InputLatencyMs <= 0 {
		t.Error("input latency must be positive")
	}
}

func TestBenchRejectsNonPositiveDuration(t *testing.T) {
	if _, err := Run(0, 100, 30, render.Color256); err == nil {
		t.Error("zero duration must fail")
	}
	var stderr bytes.Buffer
	if got := benchMain([]string{"--duration", "-1"}, &bytes.Buffer{}, &stderr); got != 1 {
		t.Errorf("exit = %d, want 1", got)
	}
	if got := benchMain([]string{"--color", "neon"}, &bytes.Buffer{}, &stderr); got != 2 {
		t.Errorf("bad color: exit = %d, want 2", got)
	}
	if got := benchMain([]string{"--columns", "abc"}, &bytes.Buffer{}, &stderr); got != 2 {
		t.Errorf("bad columns: exit = %d, want 2", got)
	}
}
