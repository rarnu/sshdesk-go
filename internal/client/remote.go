// Package client implements the sshdesk-remote and sshdesk-split client
// commands that drive a remote SSHDESK host through ordinary OpenSSH.
package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/rylena/sshdesk-go/internal/agent"
)

var targetPattern = regexp.MustCompile(`^[A-Za-z0-9_.%+@:-]{1,255}$`)

const maxRemoteResponse = 64 * 1024 * 1024
const defaultRequestTimeout = 30.0

// TimeoutError reports an expired one-shot SSH request.
type TimeoutError struct{ Seconds float64 }

func (e TimeoutError) Error() string {
	return fmt.Sprintf("timed out after %s seconds", formatG(e.Seconds))
}

// formatG renders a float like Python's %g formatting.
func formatG(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}

// runSeam is replaceable in tests: it runs argv with stdinBytes on stdin,
// captures stdout/stderr, and enforces the timeout.
var runSeam = defaultRun

var lookPathSeam = exec.LookPath

func defaultRun(argv []string, stdinBytes []byte, timeout float64) (stdout, stderr []byte, exitCode int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	defer cancel()
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Stdin = bytes.NewReader(stdinBytes)
	var outBuffer, errBuffer bytes.Buffer
	command.Stdout = &outBuffer
	command.Stderr = &errBuffer
	runErr := command.Run()
	if ctx.Err() != nil {
		return nil, nil, -1, TimeoutError{Seconds: timeout}
	}
	if runErr != nil {
		if exitError, ok := runErr.(*exec.ExitError); ok {
			return outBuffer.Bytes(), errBuffer.Bytes(), exitError.ExitCode(), nil
		}
		return nil, nil, -1, runErr
	}
	return outBuffer.Bytes(), errBuffer.Bytes(), 0, nil
}

// remoteRequest sends one NDJSON request through ssh and validates the
// response, mirroring the Python _remote_request.
func remoteRequest(target string, request map[string]any, timeout float64) (map[string]any, error) {
	ssh, err := lookPathSeam("ssh")
	if err != nil {
		return nil, fmt.Errorf("OpenSSH ssh is required")
	}
	payload := append(marshalJSON(request), '\n')
	stdout, stderr, exitCode, err := runSeam([]string{ssh, target, "sshdesk-agent", "session"}, payload, timeout)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			detail = fmt.Sprintf("SSH exited with status %d", exitCode)
		}
		return nil, fmt.Errorf("%s", detail)
	}
	if len(stdout) > maxRemoteResponse {
		return nil, fmt.Errorf("remote response exceeded the safety limit")
	}
	var response map[string]any
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("remote returned an invalid agent response")
	}
	if response == nil {
		return nil, fmt.Errorf("remote returned an invalid agent response")
	}
	if ok, _ := response["ok"].(bool); !ok {
		detail := "remote action failed"
		if raw, present := response["error"]; present {
			detail = fmt.Sprintf("%v", raw)
		}
		return nil, fmt.Errorf("%s", detail)
	}
	return response, nil
}

// marshalJSON renders one request as compact JSON.
func marshalJSON(request map[string]any) []byte {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(request)
	return bytes.TrimRight(buffer.Bytes(), "\n")
}

type remoteArgs struct {
	target     string
	timeout    float64
	command    string
	commandArg []string
}

// parseRemoteArgv splits target/--timeout from the trailing subcommand,
// accepting the option before or after the target like argparse does.
func parseRemoteArgv(argv []string) (remoteArgs, error) {
	args := remoteArgs{timeout: defaultRequestTimeout}
	var bare []string
	for index := 0; index < len(argv); index++ {
		token := argv[index]
		if token == "--timeout" {
			if index+1 >= len(argv) {
				return args, usageErr("argument --timeout: expected one argument")
			}
			index++
			parsed, err := strconv.ParseFloat(argv[index], 64)
			if err != nil {
				return args, usageErr(fmt.Sprintf("argument --timeout: invalid float value: %q", argv[index]))
			}
			args.timeout = parsed
			continue
		}
		if strings.HasPrefix(token, "--timeout=") {
			parsed, err := strconv.ParseFloat(strings.TrimPrefix(token, "--timeout="), 64)
			if err != nil {
				return args, usageErr(fmt.Sprintf("argument --timeout: invalid float value: %q", strings.TrimPrefix(token, "--timeout=")))
			}
			args.timeout = parsed
			continue
		}
		bare = append(bare, token)
	}
	if len(bare) < 1 {
		return args, usageErr("the following arguments are required: target, command")
	}
	if len(bare) < 2 {
		return args, usageErr("the following arguments are required: command")
	}
	args.target = bare[0]
	args.command = bare[1]
	args.commandArg = bare[2:]
	return args, nil
}

type usageError struct{ msg string }

func usageErr(msg string) usageError { return usageError{msg} }
func (e usageError) Error() string   { return e.msg }

// RemoteMain runs the sshdesk-remote command.
func RemoteMain(argv []string) int {
	return remoteMain(argv, os.Stdin, os.Stdout, os.Stderr)
}

func remoteMain(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	args, err := parseRemoteArgv(argv)
	if err != nil {
		fmt.Fprintf(stderr, "sshdesk-remote: %s\n", err)
		return 2
	}
	if !targetPattern.MatchString(args.target) {
		fmt.Fprintln(stderr, "sshdesk-remote: target contains unsupported characters")
		return 2
	}
	if args.timeout <= 0 {
		fmt.Fprintln(stderr, "sshdesk-remote: --timeout must be a positive number of seconds")
		return 2
	}

	if args.command == "session" {
		if len(args.commandArg) > 0 {
			fmt.Fprintf(stderr, "sshdesk-remote: unrecognized arguments: %s\n", strings.Join(args.commandArg, " "))
			return 2
		}
		ssh, err := lookPathSeam("ssh")
		if err != nil {
			fmt.Fprintln(stderr, "sshdesk-remote: OpenSSH ssh is required")
			return 2
		}
		return runInteractive(ssh, args.target, stdin, stdout, stderr)
	}

	request, output, err := buildRequest(args.command, args.commandArg)
	if err != nil {
		fmt.Fprintf(stderr, "sshdesk-remote: %s\n", err)
		return 2
	}
	response, err := remoteRequest(args.target, request, args.timeout)
	if err != nil {
		if timeout, ok := err.(TimeoutError); ok {
			fmt.Fprintf(stderr, "sshdesk-remote: timed out after %s seconds\n", formatG(timeout.Seconds))
			return 1
		}
		fmt.Fprintf(stderr, "sshdesk-remote: %s\n", err)
		return 1
	}

	switch args.command {
	case "info":
		delete(response, "id")
		delete(response, "ok")
		indented, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "sshdesk-remote: %s\n", err)
			return 1
		}
		stdout.Write(indented)
		stdout.Write([]byte("\n"))
	case "screenshot", "observe":
		raw, ok := response["image_base64"]
		if !ok {
			fmt.Fprintln(stderr, "sshdesk-remote: 'image_base64'")
			return 1
		}
		encoded, ok := raw.(string)
		if !ok {
			fmt.Fprintln(stderr, "sshdesk-remote: remote returned an invalid agent response")
			return 1
		}
		image, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			fmt.Fprintf(stderr, "sshdesk-remote: %s\n", err)
			return 1
		}
		if output == "-" {
			stdout.Write(image)
		} else if err := os.WriteFile(output, image, 0o644); err != nil {
			fmt.Fprintf(stderr, "sshdesk-remote: %s\n", err)
			return 1
		}
	}
	return 0
}

// runInteractive execs ssh with the console attached for the session
// subcommand and propagates its exit code.
func runInteractive(ssh, target string, stdin io.Reader, stdout, stderr io.Writer) int {
	command := exec.Command(ssh, target, "sshdesk-agent", "session")
	command.Stdin = stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode()
		}
		fmt.Fprintf(stderr, "sshdesk-remote: %s\n", err)
		return 1
	}
	return 0
}

var remoteCommands = map[string]bool{
	"info": true, "screenshot": true, "observe": true, "move": true,
	"click": true, "scroll": true, "type": true, "key": true,
}

// buildRequest assembles the one-shot NDJSON request from a subcommand; the
// second return value is the local screenshot output path.
func buildRequest(command string, args []string) (map[string]any, string, error) {
	if !remoteCommands[command] {
		return nil, "", usageErr(fmt.Sprintf("argument command: invalid choice: %q", command))
	}
	request := map[string]any{"id": 1, "action": command}
	switch command {
	case "info":
		if err := rejectPositionals(args); err != nil {
			return nil, "", err
		}
	case "screenshot", "observe":
		positionals, values, err := parseOptionals(args, map[string]bool{"max-width": true, "output": true})
		if err != nil {
			return nil, "", err
		}
		if err := rejectPositionals(positionals); err != nil {
			return nil, "", err
		}
		maxWidth := 0
		if raw, ok := values["max-width"]; ok {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				return nil, "", usageErr(fmt.Sprintf("argument --max-width: invalid int value: %q", raw))
			}
			maxWidth = parsed
		}
		if maxWidth < 0 || maxWidth > 4096 {
			return nil, "", usageErr("--max-width must be between 0 and 4096")
		}
		output := values["output"]
		if output == "" {
			output = "-"
		}
		request["max_width"] = maxWidth
		return request, output, nil
	case "move":
		coords, err := fixedInts(args, "x", "y")
		if err != nil {
			return nil, "", err
		}
		request["x"], request["y"] = coords[0], coords[1]
	case "click":
		positionals, values, err := parseOptionals(args, map[string]bool{"button": true, "count": true})
		if err != nil {
			return nil, "", err
		}
		coords, err := intsFromPositionals(positionals, "x", "y")
		if err != nil {
			return nil, "", err
		}
		button := values["button"]
		if button == "" {
			button = "left"
		}
		if button != "left" && button != "middle" && button != "right" {
			return nil, "", usageErr(fmt.Sprintf("argument --button: invalid choice: %q (choose from 'left', 'middle', 'right')", button))
		}
		count := 1
		if raw, ok := values["count"]; ok {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				return nil, "", usageErr(fmt.Sprintf("argument --count: invalid int value: %q", raw))
			}
			count = parsed
		}
		request["x"], request["y"] = coords[0], coords[1]
		request["button"], request["count"] = button, count
	case "scroll":
		values, err := fixedInts(args, "amount", "x", "y")
		if err != nil {
			return nil, "", err
		}
		request["amount"], request["x"], request["y"] = values[0], values[1], values[2]
	case "type":
		positionals, values, err := parseOptionals(args, map[string]bool{"interval-ms": true})
		if err != nil {
			return nil, "", err
		}
		if len(positionals) < 1 {
			return nil, "", usageErr("the following arguments are required: text")
		}
		if len(positionals) > 1 {
			return nil, "", usageErr(fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals[1:], " ")))
		}
		interval := 0.0
		if raw, ok := values["interval-ms"]; ok {
			parsed, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return nil, "", usageErr(fmt.Sprintf("argument --interval-ms: invalid float value: %q", raw))
			}
			interval = parsed
		}
		request["text"], request["interval_ms"] = positionals[0], interval
	case "key":
		positionals, flags, err := parseKeyArgs(args)
		if err != nil {
			return nil, "", err
		}
		if len(positionals) < 1 {
			return nil, "", usageErr("the following arguments are required: key")
		}
		if len(positionals) > 1 {
			return nil, "", usageErr(fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals[1:], " ")))
		}
		if _, ok := agent.KeyNames()[positionals[0]]; !ok {
			return nil, "", usageErr(fmt.Sprintf("argument key: invalid choice: %q", positionals[0]))
		}
		request["key"] = positionals[0]
		request["ctrl"], request["alt"], request["shift"] = flags["ctrl"], flags["alt"], flags["shift"]
	}
	return request, "", nil
}

func rejectPositionals(positionals []string) error {
	if len(positionals) > 0 {
		return usageErr(fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals, " ")))
	}
	return nil
}

// parseOptionals separates positionals from --name value / --name=value
// options.
func parseOptionals(args []string, valueFlags map[string]bool) (positionals []string, values map[string]string, err error) {
	values = make(map[string]string)
	for index := 0; index < len(args); index++ {
		token := args[index]
		if !strings.HasPrefix(token, "--") {
			positionals = append(positionals, token)
			continue
		}
		name := token[2:]
		if eq := strings.Index(name, "="); eq >= 0 {
			if !valueFlags[name[:eq]] {
				return nil, nil, usageErr(fmt.Sprintf("unrecognized arguments: %s", token))
			}
			values[name[:eq]] = name[eq+1:]
			continue
		}
		if !valueFlags[name] {
			return nil, nil, usageErr(fmt.Sprintf("unrecognized arguments: --%s", name))
		}
		if index+1 >= len(args) {
			return nil, nil, usageErr(fmt.Sprintf("argument --%s: expected one argument", name))
		}
		index++
		values[name] = args[index]
	}
	return positionals, values, nil
}

func parseKeyArgs(args []string) (positionals []string, flags map[string]bool, err error) {
	flags = make(map[string]bool)
	for _, token := range args {
		if strings.HasPrefix(token, "--") {
			name := strings.TrimPrefix(token, "--")
			if name != "ctrl" && name != "alt" && name != "shift" {
				return nil, nil, usageErr(fmt.Sprintf("unrecognized arguments: %s", token))
			}
			flags[name] = true
			continue
		}
		positionals = append(positionals, token)
	}
	return positionals, flags, nil
}

func intsFromPositionals(positionals []string, names ...string) ([]int, error) {
	if len(positionals) < len(names) {
		return nil, usageErr(fmt.Sprintf("the following arguments are required: %s", strings.Join(names[len(positionals):], ", ")))
	}
	if len(positionals) > len(names) {
		return nil, usageErr(fmt.Sprintf("unrecognized arguments: %s", strings.Join(positionals[len(names):], " ")))
	}
	result := make([]int, len(names))
	for index, raw := range positionals {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return nil, usageErr(fmt.Sprintf("argument %s: invalid int value: %q", names[index], raw))
		}
		result[index] = parsed
	}
	return result, nil
}

func fixedInts(args []string, names ...string) ([]int, error) {
	positionals, _, err := parseOptionals(args, nil)
	if err != nil {
		return nil, err
	}
	return intsFromPositionals(positionals, names...)
}
