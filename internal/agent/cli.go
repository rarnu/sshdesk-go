package agent

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// usageError marks command-line violations, which exit 2 like argparse.
type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

// parseTokens separates positionals from --flag tokens. valueFlags take one
// argument (--name value or --name=value); boolFlags store true.
func parseTokens(args []string, valueFlags, boolFlags map[string]bool) (positionals []string, values map[string]string, flags map[string]bool, err error) {
	values = make(map[string]string)
	flags = make(map[string]bool)
	for index := 0; index < len(args); index++ {
		token := args[index]
		if token == "--" {
			positionals = append(positionals, args[index+1:]...)
			return
		}
		if !strings.HasPrefix(token, "--") {
			positionals = append(positionals, token)
			continue
		}
		name := token[2:]
		if eq := strings.Index(name, "="); eq >= 0 {
			key := name[:eq]
			if !valueFlags[key] {
				return nil, nil, nil, usageError{fmt.Sprintf("unrecognized arguments: %s", token)}
			}
			values[key] = name[eq+1:]
			continue
		}
		if boolFlags[name] {
			flags[name] = true
			continue
		}
		if valueFlags[name] {
			if index+1 >= len(args) {
				return nil, nil, nil, usageError{fmt.Sprintf("argument --%s: expected one argument", name)}
			}
			index++
			values[name] = args[index]
			continue
		}
		return nil, nil, nil, usageError{fmt.Sprintf("unrecognized arguments: --%s", name)}
	}
	return
}

func intFlag(values map[string]string, name string, fallback int) (int, error) {
	raw, ok := values[name]
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return 0, usageError{fmt.Sprintf("argument --%s: invalid int value: %q", name, raw)}
	}
	return parsed, nil
}

func floatFlag(values map[string]string, name string, fallback float64) (float64, error) {
	raw, ok := values[name]
	if !ok {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, usageError{fmt.Sprintf("argument --%s: invalid float value: %q", name, raw)}
	}
	return parsed, nil
}

func positionalInts(positionals []string, names ...string) ([]int, error) {
	if len(positionals) < len(names) {
		return nil, usageError{fmt.Sprintf("the following arguments are required: %s", strings.Join(names[len(positionals):], ", "))}
	}
	if len(positionals) > len(names) {
		return nil, usageError{fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals[len(names):], " "))}
	}
	result := make([]int, len(names))
	for index, raw := range positionals {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, usageError{fmt.Sprintf("argument %s: invalid int value: %q", names[index], raw)}
		}
		result[index] = parsed
	}
	return result, nil
}

func noPositionals(positionals []string) error {
	if len(positionals) > 0 {
		return usageError{fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals, " "))}
	}
	return nil
}

// AgentMain runs the sshdesk-agent command set.
func AgentMain(argv []string) int {
	return agentMain(argv, os.Stdout, os.Stderr)
}

func agentMain(argv []string, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		fmt.Fprintln(stderr, "sshdesk-agent: the following arguments are required: command")
		return 2
	}
	command, args := argv[0], argv[1:]
	for _, help := range []string{"-h", "--help"} {
		if command == help {
			fmt.Fprintln(stdout, "usage: sshdesk-agent {info,screenshot,observe,move,click,scroll,type,key,session}")
			return 0
		}
	}
	code, err := runAgentCommand(command, args, stdout)
	if err != nil {
		if usage, ok := err.(usageError); ok {
			fmt.Fprintf(stderr, "sshdesk-agent: %s\n", usage.msg)
			return 2
		}
		fmt.Fprintf(stderr, "sshdesk-agent: %s\n", err)
		return 1
	}
	return code
}

func runAgentCommand(command string, args []string, stdout io.Writer) (int, error) {
	controller := NewController()
	defer controller.Close()
	switch command {
	case "info":
		positionals, _, _, err := parseTokens(args, nil, nil)
		if err != nil {
			return 0, err
		}
		if err := noPositionals(positionals); err != nil {
			return 0, err
		}
		info, err := controller.Info()
		if err != nil {
			return 0, err
		}
		stdout.Write(marshalCompact(info))
		stdout.Write([]byte("\n"))
		return 0, nil
	case "screenshot", "observe":
		positionals, values, _, err := parseTokens(args, map[string]bool{"max-width": true, "output": true}, nil)
		if err != nil {
			return 0, err
		}
		if err := noPositionals(positionals); err != nil {
			return 0, err
		}
		maxWidth, err := intFlag(values, "max-width", 0)
		if err != nil {
			return 0, err
		}
		if maxWidth < 0 || maxWidth > 4096 {
			return 0, fmt.Errorf("--max-width must be between 0 and 4096")
		}
		output := values["output"]
		if output == "" {
			output = "-"
		}
		image, _, _, err := controller.Screenshot(maxWidth)
		if err != nil {
			return 0, err
		}
		if output == "-" {
			if _, err := stdout.Write(image); err != nil {
				return 0, err
			}
			return 0, nil
		}
		if err := os.WriteFile(output, image, 0o644); err != nil {
			return 0, err
		}
		return 0, nil
	case "move":
		coords, err := twoCoordinates(args)
		if err != nil {
			return 0, err
		}
		return 0, controller.Move(coords[0], coords[1])
	case "click":
		positionals, values, _, err := parseTokens(args, map[string]bool{"button": true, "count": true}, nil)
		if err != nil {
			return 0, err
		}
		coords, err := positionalInts(positionals, "x", "y")
		if err != nil {
			return 0, err
		}
		x, err := clampCoordinate(coords[0])
		if err != nil {
			return 0, err
		}
		y, err := clampCoordinate(coords[1])
		if err != nil {
			return 0, err
		}
		button := values["button"]
		if button == "" {
			button = "left"
		}
		if _, ok := buttons[button]; !ok {
			return 0, usageError{fmt.Sprintf("argument --button: invalid choice: %q (choose from 'left', 'middle', 'right')", button)}
		}
		count, err := intFlag(values, "count", 1)
		if err != nil {
			return 0, err
		}
		return 0, controller.Click(x, y, button, count)
	case "scroll":
		positionals, _, _, err := parseTokens(args, nil, nil)
		if err != nil {
			return 0, err
		}
		values, err := positionalInts(positionals, "amount", "x", "y")
		if err != nil {
			return 0, err
		}
		x, err := clampCoordinate(values[1])
		if err != nil {
			return 0, err
		}
		y, err := clampCoordinate(values[2])
		if err != nil {
			return 0, err
		}
		return 0, controller.Scroll(values[0], x, y)
	case "type":
		positionals, values, _, err := parseTokens(args, map[string]bool{"interval-ms": true}, nil)
		if err != nil {
			return 0, err
		}
		if len(positionals) < 1 {
			return 0, usageError{"the following arguments are required: text"}
		}
		if len(positionals) > 1 {
			return 0, usageError{fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals[1:], " "))}
		}
		interval, err := floatFlag(values, "interval-ms", 0)
		if err != nil {
			return 0, err
		}
		return 0, controller.TypeText(positionals[0], interval)
	case "key":
		positionals, _, flags, err := parseTokens(args, nil, map[string]bool{"ctrl": true, "alt": true, "shift": true})
		if err != nil {
			return 0, err
		}
		if len(positionals) < 1 {
			return 0, usageError{"the following arguments are required: key"}
		}
		if len(positionals) > 1 {
			return 0, usageError{fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals[1:], " "))}
		}
		name := positionals[0]
		if _, ok := keyNames[name]; !ok {
			return 0, usageError{fmt.Sprintf("argument key: invalid choice: %q", name)}
		}
		return 0, controller.Key(name, modifiers(func(name string) bool { return flags[name] }))
	case "session":
		positionals, _, _, err := parseTokens(args, nil, nil)
		if err != nil {
			return 0, err
		}
		if err := noPositionals(positionals); err != nil {
			return 0, err
		}
		return RunSession(controller, os.Stdin, stdout), nil
	}
	return 0, usageError{fmt.Sprintf("argument command: invalid choice: %q", command)}
}

func twoCoordinates(args []string) ([]int, error) {
	positionals, _, _, err := parseTokens(args, nil, nil)
	if err != nil {
		return nil, err
	}
	coords, err := positionalInts(positionals, "x", "y")
	if err != nil {
		return nil, err
	}
	x, err := clampCoordinate(coords[0])
	if err != nil {
		return nil, err
	}
	y, err := clampCoordinate(coords[1])
	if err != nil {
		return nil, err
	}
	return []int{x, y}, nil
}

func clampCoordinate(value int) (int, error) {
	if value < -16384 || value > 65535 {
		return 0, fmt.Errorf("coordinate is outside the supported range")
	}
	return value, nil
}

// AgentSSHMain runs the sshdesk-agent-ssh allowlist wrapper.
func AgentSSHMain(argv []string) int {
	return agentSSH(argv, os.Stderr)
}

func agentSSH(argv []string, stderr io.Writer) int {
	if len(argv) != 1 || len(argv[0]) > maxCommandLength {
		fmt.Fprintln(stderr, "sshdesk-agent: invalid SSH command")
		return 2
	}
	command, err := ShlexSplit(argv[0])
	if err != nil {
		fmt.Fprintf(stderr, "sshdesk-agent: invalid SSH command: %s\n", err)
		return 2
	}
	if len(command) == 0 || filepath.Base(command[0]) != "sshdesk-agent" {
		fmt.Fprintln(stderr, "This account accepts only SSHDESK desktop, shell selector, or agent commands.")
		return 126
	}
	for _, value := range command[1:] {
		if value == "--output" || strings.HasPrefix(value, "--output=") {
			fmt.Fprintln(stderr, "sshdesk-agent: remote screenshots are written only to stdout")
			return 2
		}
	}
	return agentMain(command[1:], os.Stdout, stderr)
}
