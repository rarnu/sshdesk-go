package client

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var panePattern = regexp.MustCompile(`^%[0-9]+$`)

// tmuxRun is a test seam: it runs tmux with args and returns the exit code.
var tmuxRun = defaultTmuxRun

var tmuxGetenv = os.Getenv

var tmuxGetpid = os.Getpid

func defaultTmuxRun(tmux string, args ...string) int {
	command := exec.Command(tmux, args...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		return 1
	}
	return 0
}

// splitArguments builds the tmux split-window vector. The pane runs the
// explicit desktop selector so it stays a visual session under the
// standard-SSH forced-command routing.
func splitArguments(target, direction string, size int, pane string) []string {
	sideways := direction == "left" || direction == "right"
	arguments := []string{"split-window"}
	if sideways {
		arguments = append(arguments, "-h")
	} else {
		arguments = append(arguments, "-v")
	}
	if direction == "left" || direction == "up" {
		arguments = append(arguments, "-b")
	}
	arguments = append(arguments, "-p", strconv.Itoa(size), "-t", pane, "--", "ssh", "-t", target, "desktop")
	return arguments
}

// SplitMain runs the sshdesk-split command.
func SplitMain(argv []string) int {
	return splitMain(argv, os.Stderr)
}

func splitMain(argv []string, stderr io.Writer) int {
	target, direction, size, err := parseSplitArgv(argv)
	if err != nil {
		fmt.Fprintf(stderr, "sshdesk-split: %s\n", err)
		return 2
	}
	if !targetPattern.MatchString(target) {
		fmt.Fprintln(stderr, "sshdesk-split: target contains unsupported characters")
		return 2
	}
	if size < 20 || size > 80 {
		fmt.Fprintln(stderr, "sshdesk-split: --size must be between 20 and 80")
		return 2
	}
	tmux, err := lookPathSeam("tmux")
	if err != nil {
		fmt.Fprintln(stderr, "sshdesk-split: tmux is required for portable split-pane mode")
		return 2
	}

	if tmuxGetenv("TMUX") != "" {
		pane := tmuxGetenv("TMUX_PANE")
		if !panePattern.MatchString(pane) {
			fmt.Fprintln(stderr, "sshdesk-split: cannot identify the current tmux pane")
			return 2
		}
		if code := tmuxRun(tmux, "set-option", "-p", "-t", pane, "allow-passthrough", "on"); code != 0 {
			fmt.Fprintln(stderr, "sshdesk-split: could not enable tmux allow-passthrough")
			return 1
		}
		return tmuxRun(tmux, splitArguments(target, direction, size, pane)...)
	}

	session := fmt.Sprintf("sshdesk-%d", tmuxGetpid())
	if code := tmuxRun(tmux, "new-session", "-d", "-s", session); code != 0 {
		fmt.Fprintln(stderr, "sshdesk-split: could not create the tmux session")
		return 1
	}
	steps := [][]string{
		{"set-option", "-t", session, "allow-passthrough", "on"},
		{"set-option", "-t", session, "focus-events", "on"},
		splitArguments(target, direction, size, session+":0.0"),
	}
	for _, step := range steps {
		if code := tmuxRun(tmux, step...); code != 0 {
			tmuxRun(tmux, "kill-session", "-t", session)
			fmt.Fprintln(stderr, "sshdesk-split: could not prepare the tmux session")
			return 1
		}
	}
	return tmuxRun(tmux, "attach-session", "-t", session)
}

func parseSplitArgv(argv []string) (target, direction string, size int, err error) {
	direction = "right"
	size = 50
	var positionals []string
	values := map[string]string{}
	for index := 0; index < len(argv); index++ {
		token := argv[index]
		if !strings.HasPrefix(token, "--") {
			positionals = append(positionals, token)
			continue
		}
		name := token[2:]
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			values[name[:eq]] = name[eq+1:]
			continue
		}
		if name != "direction" && name != "size" {
			return "", "", 0, usageErr(fmt.Sprintf("unrecognized arguments: %s", token))
		}
		if index+1 >= len(argv) {
			return "", "", 0, usageErr(fmt.Sprintf("argument --%s: expected one argument", name))
		}
		index++
		values[name] = argv[index]
	}
	if raw, ok := values["direction"]; ok {
		direction = raw
	}
	if direction != "right" && direction != "left" && direction != "down" && direction != "up" {
		return "", "", 0, usageErr(fmt.Sprintf("argument --direction: invalid choice: %q (choose from 'right', 'left', 'down', 'up')", direction))
	}
	if raw, ok := values["size"]; ok {
		parsed, convErr := strconv.Atoi(raw)
		if convErr != nil {
			return "", "", 0, usageErr(fmt.Sprintf("argument --size: invalid int value: %q", raw))
		}
		size = parsed
	}
	if len(positionals) < 1 {
		return "", "", 0, usageErr("the following arguments are required: target")
	}
	if len(positionals) > 1 {
		return "", "", 0, usageErr(fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals[1:], " ")))
	}
	return positionals[0], direction, size, nil
}
