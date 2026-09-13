// Package forcedcmd routes the OpenSSH ForceCommand: desktop session, login
// shell selector, or the agent allowlist, with optional RUN_AS elevation via
// sudo -n (fixed argument vectors, never a shell).
package forcedcmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rylena/sshdesk-go/internal/agent"
	"github.com/rylena/sshdesk-go/internal/config"
)

// Deps wires the route side effects so tests can dry-run the dispatcher.
type Deps struct {
	Getenv       func(string) string
	Account      func() (string, error)
	Config       func() (map[string]string, error)
	HasTerminal  func() bool
	Exec         func(argv []string) (int, error)
	ServerMain   func() int
	AgentSSHMain func(argv []string) int
	LoginShell   func() (string, error)
	SelfPath     func() (string, error)
	Stderr       io.Writer
}

var desktopCommands = map[string]bool{
	"desktop":        true,
	"sshdesk":        true,
	"sshdesk-server": true,
}

var shellCommands = map[string]bool{
	"shell":         true,
	"sshdesk-shell": true,
}

// Main dispatches on SSH_ORIGINAL_COMMAND.
func Main(d Deps) int {
	original := d.Getenv("SSH_ORIGINAL_COMMAND")
	if original == "" {
		if !d.HasTerminal() {
			fmt.Fprintln(d.Stderr, "SSHDESK requires an interactive SSH terminal (PTY).")
			return 1
		}
		return d.serverRoute()
	}
	command, _ := agent.ShlexSplit(original)
	program := ""
	if len(command) > 0 {
		program = filepath.Base(command[0])
	}
	if program == "sshdesk-agent" {
		return d.agentRoute(original)
	}
	if len(command) == 1 && desktopCommands[command[0]] {
		if !d.HasTerminal() {
			fmt.Fprintln(d.Stderr, "SSHDESK requires an interactive SSH terminal (PTY).")
			return 1
		}
		return d.serverRoute()
	}
	if len(command) == 1 && shellCommands[command[0]] {
		if !d.HasTerminal() {
			fmt.Fprintln(d.Stderr, "The SSH shell selector requires an interactive terminal (PTY).")
			return 1
		}
		return d.shellRoute()
	}
	return d.agentRoute(original)
}

// environment resolves the account, the whitelisted configuration, and
// whether the desktop paths must elevate through sudo.
func (d Deps) environment() (account string, values map[string]string, elevate bool, ok bool) {
	values, err := d.Config()
	if err != nil {
		fmt.Fprintln(d.Stderr, "Invalid SSHDESK desktop account.")
		return "", nil, false, false
	}
	account, err = d.Account()
	if err != nil {
		fmt.Fprintf(d.Stderr, "sshdesk: could not determine the current account: %s\n", err)
		return "", nil, false, false
	}
	return account, values, values["RUN_AS"] != account, true
}

// applyEnv exports the resolved whitelist values for the child session,
// mirroring the shell wrapper's export block.
func (d Deps) applyEnv(values map[string]string) {
	for _, key := range config.Keys {
		if value := values[key]; value != "" {
			os.Setenv(key, value)
		}
	}
}

func (d Deps) serverRoute() int {
	_, values, elevate, ok := d.environment()
	if !ok {
		return 1
	}
	if elevate {
		self, err := d.SelfPath()
		if err != nil {
			fmt.Fprintf(d.Stderr, "sshdesk: %s\n", err)
			return 1
		}
		return d.execOrReport([]string{"/usr/bin/sudo", "-n", "-u", values["RUN_AS"], "--", self, "server"})
	}
	d.applyEnv(values)
	return d.ServerMain()
}

func (d Deps) agentRoute(original string) int {
	_, values, elevate, ok := d.environment()
	if !ok {
		return 1
	}
	if elevate {
		self, err := d.SelfPath()
		if err != nil {
			fmt.Fprintf(d.Stderr, "sshdesk: %s\n", err)
			return 1
		}
		return d.execOrReport([]string{"/usr/bin/sudo", "-n", "-u", values["RUN_AS"], "--", self, "agent-ssh", original})
	}
	d.applyEnv(values)
	return d.AgentSSHMain([]string{original})
}

// shellRoute execs the authenticated account's own login shell. It never
// elevates through RUN_AS/sudo.
func (d Deps) shellRoute() int {
	shell, err := d.LoginShell()
	if err != nil {
		fmt.Fprintf(d.Stderr, "sshdesk-shell: %s\n", err)
		return 1
	}
	return d.execOrReport(shellArgv(shell))
}

func (d Deps) execOrReport(argv []string) int {
	code, err := d.Exec(argv)
	if err != nil {
		fmt.Fprintf(d.Stderr, "sshdesk: %s\n", err)
		return 1
	}
	return code
}

// shellArgv builds the exec vector for a login shell: POSIX shells become
// login shells through a dash-prefixed argv[0]; cmd.exe/PowerShell have no
// login mode.
func shellArgv(shell string) []string {
	if isWindows {
		return []string{shell}
	}
	base := filepath.Base(shell)
	if !strings.HasPrefix(base, "-") {
		base = "-" + base
	}
	return []string{base}
}
