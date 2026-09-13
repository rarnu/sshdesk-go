// Package bench measures ANSI rendering bandwidth and terminal parser
// latency against the synthetic desktop.
package bench

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rylena/sshdesk-go/internal/capture/synthetic"
	terminalparser "github.com/rylena/sshdesk-go/internal/input/terminal"
	"github.com/rylena/sshdesk-go/internal/render"
	"github.com/rylena/sshdesk-go/internal/render/ansi"
)

// Result mirrors the Python BenchmarkResult dataclass.
type Result struct {
	Duration                 float64
	AverageFPS               float64
	AverageBandwidthKbit     float64
	PeakBandwidthKbit        float64
	InputLatencyMs           float64
	FullFrames               int
	DeltaFrames              int
	AverageChangedPercentage float64
}

func inputLatency(iterations int) (float64, error) {
	parser := &terminalparser.Parser{}
	var total float64
	for index := 0; index < iterations; index++ {
		started := time.Now()
		events, err := parser.FeedNow([]byte("\x1b[A"))
		if err != nil {
			return 0, err
		}
		if len(events) != 1 {
			return 0, fmt.Errorf("terminal parser benchmark failed")
		}
		total += float64(time.Since(started)) / 1e6
	}
	return total / float64(iterations), nil
}

// Run executes the benchmark loop at a 30 FPS target.
func Run(duration float64, columns, rows int, color render.ColorMode) (Result, error) {
	if duration <= 0 {
		return Result{}, fmt.Errorf("duration must be positive")
	}
	capture := synthetic.NewCapture(1920, 1080, true)
	defer capture.Close()
	renderer, err := ansi.NewRenderer(0.60, 0, 1.0)
	if err != nil {
		return Result{}, err
	}
	writer := ansi.NewWriter(render.Capabilities{
		Term:     "benchmark",
		Color:    color,
		Mouse:    true,
		SGRMouse: true,
		Unicode:  true,
	}, "SSHDESK")
	var previous *render.RenderedFrame
	started := time.Now()
	deadline := started.Add(time.Duration(duration * float64(time.Second)))
	nextFrame := started
	frameCount, fullFrames, deltaFrames, totalBytes := 0, 0, 0, 0
	changedTotal := 0.0
	buckets := make(map[int]int)
	for time.Now().Before(deadline) {
		now := time.Now()
		if now.Before(nextFrame) {
			time.Sleep(min(nextFrame.Sub(now), 10*time.Millisecond))
			continue
		}
		nextFrame = nextFrame.Add(time.Second / 30)
		frame, err := capture.Capture()
		if err != nil {
			return Result{}, err
		}
		rendered := renderer.Render(frame, columns, rows)
		update := renderer.Diff(previous, rendered)
		previous = rendered
		if update.Kind == render.UpdateUnchanged {
			continue
		}
		packetSize := len(writer.Update(update))
		second := int(now.Sub(started).Seconds())
		buckets[second] += packetSize
		totalBytes += packetSize
		frameCount++
		changedTotal += update.ChangedPercentage()
		if update.Kind == render.UpdateFull {
			fullFrames++
		} else {
			deltaFrames++
		}
	}
	elapsed := time.Since(started).Seconds()
	peakRate := 0.0
	for second, byteCount := range buckets {
		bucketDuration := max(1e-9, min(elapsed, float64(second)+1.0)-float64(second))
		peakRate = max(peakRate, float64(byteCount)*8/bucketDuration/1000)
	}
	latency, err := inputLatency(100)
	if err != nil {
		return Result{}, err
	}
	averageChanged := 0.0
	if frameCount > 0 {
		averageChanged = changedTotal / float64(frameCount)
	}
	return Result{
		Duration:                 elapsed,
		AverageFPS:               float64(frameCount) / elapsed,
		AverageBandwidthKbit:     float64(totalBytes) * 8 / elapsed / 1000,
		PeakBandwidthKbit:        peakRate,
		InputLatencyMs:           latency,
		FullFrames:               fullFrames,
		DeltaFrames:              deltaFrames,
		AverageChangedPercentage: averageChanged,
	}, nil
}

// Print writes the report in the exact Python layout.
func Print(writer io.Writer, result Result) {
	fmt.Fprintf(writer, "Session duration:       %.1fs\n", result.Duration)
	fmt.Fprintf(writer, "Average FPS:            %.1f\n", result.AverageFPS)
	fmt.Fprintf(writer, "Average bandwidth:      %.0f Kbit/s\n", result.AverageBandwidthKbit)
	fmt.Fprintf(writer, "Peak bandwidth:         %.0f Kbit/s\n", result.PeakBandwidthKbit)
	fmt.Fprintf(writer, "Input parse latency:    %.2f ms\n", result.InputLatencyMs)
	fmt.Fprintf(writer, "Full frames:            %d\n", result.FullFrames)
	fmt.Fprintf(writer, "Delta frames:           %d\n", result.DeltaFrames)
	fmt.Fprintf(writer, "Average changed area:   %.1f%%\n", result.AverageChangedPercentage)
}

// Main runs the sshdesk-bench command.
func Main(argv []string) int {
	return benchMain(argv, os.Stdout, os.Stderr)
}

func benchMain(argv []string, stdout, stderr io.Writer) int {
	duration, columns, rows, color, err := parseBenchArgv(argv)
	if err != nil {
		fmt.Fprintf(stderr, "sshdesk-bench: %s\n", err)
		return 2
	}
	result, err := Run(duration, columns, rows, color)
	if err != nil {
		fmt.Fprintf(stderr, "sshdesk-bench: %s\n", err)
		return 1
	}
	Print(stdout, result)
	return 0
}

func parseBenchArgv(argv []string) (duration float64, columns, rows int, color render.ColorMode, err error) {
	duration, columns, rows, color = 60.0, 100, 30, render.Color256
	values := make(map[string]string)
	for index := 0; index < len(argv); index++ {
		token := argv[index]
		if !strings.HasPrefix(token, "--") {
			return 0, 0, 0, 0, fmt.Errorf("unrecognized arguments: %s", token)
		}
		name := token[2:]
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			values[name[:eq]] = name[eq+1:]
			continue
		}
		if index+1 >= len(argv) {
			return 0, 0, 0, 0, fmt.Errorf("argument --%s: expected one argument", name)
		}
		index++
		values[name] = argv[index]
	}
	for name, raw := range values {
		switch name {
		case "duration":
			parsed, convErr := strconv.ParseFloat(raw, 64)
			if convErr != nil {
				return 0, 0, 0, 0, fmt.Errorf("argument --duration: invalid float value: %q", raw)
			}
			duration = parsed
		case "columns":
			parsed, convErr := strconv.Atoi(raw)
			if convErr != nil {
				return 0, 0, 0, 0, fmt.Errorf("argument --columns: invalid int value: %q", raw)
			}
			columns = parsed
		case "rows":
			parsed, convErr := strconv.Atoi(raw)
			if convErr != nil {
				return 0, 0, 0, 0, fmt.Errorf("argument --rows: invalid int value: %q", raw)
			}
			rows = parsed
		case "color":
			switch raw {
			case "truecolor":
				color = render.ColorTruecolor
			case "256":
				color = render.Color256
			case "16":
				color = render.Color16
			default:
				return 0, 0, 0, 0, fmt.Errorf("argument --color: invalid choice: %q (choose from 'truecolor', '256', '16')", raw)
			}
		default:
			return 0, 0, 0, 0, fmt.Errorf("unrecognized arguments: --%s", name)
		}
	}
	return duration, columns, rows, color, nil
}
