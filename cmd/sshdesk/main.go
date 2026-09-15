// Command sshdesk is the single SSHDESK binary. It dispatches on the
// argv[0] basename (busybox style) or on the first argument subcommand.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/rarnu/sshdesk-go/internal/agent"
	"github.com/rarnu/sshdesk-go/internal/bench"
	"github.com/rarnu/sshdesk-go/internal/capture"
	"github.com/rarnu/sshdesk-go/internal/client"
	"github.com/rarnu/sshdesk-go/internal/forcedcmd"
	"github.com/rarnu/sshdesk-go/internal/render"
	"github.com/rarnu/sshdesk-go/internal/render/ansi"
	"github.com/rarnu/sshdesk-go/internal/session"
	"github.com/rarnu/sshdesk-go/internal/setup"
)

func main() {
	os.Exit(dispatch(os.Args))
}

func dispatch(argv []string) int {
	name := filepath.Base(argv[0])
	args := argv[1:]
	switch name {
	case "sshdesk-server":
		return serverMain(args)
	case "sshdesk-local":
		return localMain(args)
	case "sshdesk-forced-command":
		return forcedCommandMain(args)
	case "sshdesk-agent":
		return agent.AgentMain(args)
	case "sshdesk-agent-ssh":
		return agent.AgentSSHMain(args)
	case "sshdesk-remote":
		return client.RemoteMain(args)
	case "sshdesk-split":
		return client.SplitMain(args)
	case "sshdesk-bench":
		return bench.Main(args)
	case "sshdesk":
	default:
		fmt.Fprintf(os.Stderr, "sshdesk: %s is not implemented yet\n", name)
		return 1
	}
	if len(args) > 0 {
		switch args[0] {
		case "server":
			return serverMain(args[1:])
		case "local":
			return localMain(args[1:])
		case "forced-command":
			return forcedCommandMain(args[1:])
		case "agent":
			return agent.AgentMain(args[1:])
		case "agent-ssh":
			return agent.AgentSSHMain(args[1:])
		case "remote":
			return client.RemoteMain(args[1:])
		case "split":
			return client.SplitMain(args[1:])
		case "bench":
			return bench.Main(args[1:])
		case "install", "--install":
			return setup.InstallMain(args[1:], os.Stdout, os.Stderr)
		case "uninstall", "--uninstall":
			return setup.UninstallMain(args[1:], os.Stdout, os.Stderr)
		}
	}
	return serverMain(args)
}

// forcedCommandMain wires the forced-command dispatcher to the real server
// and agent entry points.
func forcedCommandMain(argv []string) int {
	if len(argv) > 0 {
		fmt.Fprintf(os.Stderr, "sshdesk-forced-command: unrecognized arguments: %s\n", strings.Join(argv, " "))
		return 2
	}
	deps := forcedcmd.DefaultDeps()
	deps.ServerMain = func() int { return serverMain(nil) }
	return forcedcmd.Main(deps)
}

func serverMain(argv []string) int {
	flags := flag.NewFlagSet("sshdesk-server", flag.ContinueOnError)
	captureName := flags.String("capture", "auto", "capture backend")
	inputName := flags.String("input", "auto", "input backend")
	display := flags.String("display", "", "X11 display (defaults to DISPLAY)")
	noInput := flags.Bool("no-input", false, "view-only session")
	syntheticStatic := flags.Bool("synthetic-static", false, "static synthetic desktop")
	color := flags.String("color", "auto", "color mode")
	noMouse := flags.Bool("no-mouse", false, "keyboard-only mode")
	asciiMode := flags.Bool("ascii", false, "avoid Unicode half-block glyphs")
	maxFPS := flags.Float64("max-fps", 0, "active refresh limit")
	scale := flags.Float64("scale", 0, "render scale from 0.25 to 1.0")
	check := flags.Bool("check", false, "verify capture and input access without starting a session")
	if err := flags.Parse(argv); err != nil {
		return 2
	}

	captureBackend, err := createCapture(*captureName, !*syntheticStatic, *display)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-server: %v\n", err)
		return 1
	}
	inputBackend, err := createInput(*inputName, *noInput, *captureName, *display, captureBackend)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-server: %v\n", err)
		captureBackend.Close()
		return 1
	}

	if *check {
		frame, err := captureBackend.Capture()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sshdesk-server: %v\n", err)
			return 1
		}
		inputState := "enabled"
		if *noInput {
			inputState = "disabled"
		}
		fmt.Printf("SSHDESK check passed: %dx%d %s capture; input=%s\n",
			frame.Width(), frame.Height(), capture.Name(captureBackend), inputState)
		return 0
	}

	environment := environmentMap()
	switch *color {
	case "auto":
	case "truecolor", "256", "16":
		environment["SSHDESK_COLOR"] = *color
	default:
		fmt.Fprintf(os.Stderr, "sshdesk-server: invalid choice: %s (choose from auto, truecolor, 256, 16)\n", *color)
		return 2
	}
	if *noMouse {
		environment["SSHDESK_MOUSE"] = "0"
	}
	if *asciiMode {
		environment["SSHDESK_UNICODE"] = "0"
	}
	capabilities, err := render.DetectCapabilities(environment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-server: %v\n", err)
		return 1
	}

	var requestedFPS *float64
	if flags.Lookup("max-fps").Value.String() != "0" || flagPassed(flags, "max-fps") {
		value := *maxFPS
		requestedFPS = &value
	}
	var requestedScale *float64
	if flagPassed(flags, "scale") {
		value := *scale
		requestedScale = &value
	}

	direct := session.NewDirectSession(captureBackend, inputBackend, capabilities, requestedFPS, requestedScale)
	return direct.Run()
}

func flagPassed(flags *flag.FlagSet, name string) bool {
	found := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func environmentMap() map[string]string {
	environment := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		environment[key] = value
	}
	return environment
}

func localMain(argv []string) int {
	flags := flag.NewFlagSet("sshdesk-local", flag.ContinueOnError)
	captureName := flags.String("capture", "synthetic", "capture backend")
	once := flags.Bool("once", false, "render once and exit")
	columns := flags.Int("columns", 0, "terminal columns")
	rows := flags.Int("rows", 0, "terminal rows")
	if err := flags.Parse(argv); err != nil {
		return 2
	}
	if *columns <= 0 || *rows <= 0 {
		width, height, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil {
			width, height = 80, 24
		}
		if *columns <= 0 {
			*columns = width
		}
		if *rows <= 0 {
			*rows = height
		}
	}
	captureBackend, err := createCapture(*captureName, true, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-local: %v\n", err)
		return 1
	}
	defer captureBackend.Close()
	frame, err := captureBackend.Capture()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-local: %v\n", err)
		return 1
	}
	renderer, err := ansi.NewRenderer(0.60, 0, 1.0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-local: %v\n", err)
		return 1
	}
	rendered := renderer.Render(frame, *columns, *rows)
	capabilities, err := render.DetectCapabilities(environmentMap())
	if err != nil {
		fmt.Fprintf(os.Stderr, "sshdesk-local: %v\n", err)
		return 1
	}
	writer := ansi.NewWriter(capabilities, "SSHDESK")
	interactive := !*once && term.IsTerminal(int(os.Stdout.Fd()))
	if interactive {
		os.Stdout.Write(writer.Enter())
	}
	os.Stdout.Write(writer.Full(rendered))
	if interactive {
		os.Stdout.Write(writer.Leave())
	}
	return 0
}
