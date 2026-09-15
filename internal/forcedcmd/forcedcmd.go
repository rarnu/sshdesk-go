// Package forcedcmd routes the OpenSSH ForceCommand. Only the exact "desktop"
// selector starts the desktop session; every other connection behaves exactly
// like standard SSH: no command runs the authenticated account's login shell,
// and any other command is passed verbatim to that shell's -c. sudo -n RUN_AS
// elevation applies to the desktop path only.
package forcedcmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rarnu/sshdesk-go/internal/config"
)

// Deps wires the route side effects so tests can dry-run the dispatcher.
type Deps struct {
	Getenv      func(string) string
	Account     func() (string, error)
	Config      func() (map[string]string, error)
	HasTerminal func() bool
	Exec        func(argv []string) (int, error)
	ServerMain  func() int
	LoginShell  func() (string, error)
	SelfPath    func() (string, error)
	Stderr      io.Writer
}

// Main dispatches on SSH_ORIGINAL_COMMAND. The whitelist configuration is
// loaded and exported on every path before dispatch (file overrides
// environment, RUN_AS validated).
func Main(d Deps) int {
	values, elevate, ok := d.environment()
	if !ok {
		return 1
	}
	original := d.Getenv("SSH_ORIGINAL_COMMAND")
	if original == "desktop" {
		if !d.HasTerminal() {
			fmt.Fprintln(d.Stderr, "SSHDESK requires an interactive SSH terminal (PTY).")
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
	d.applyEnv(values)
	return d.shellRoute(original)
}

// environment resolves the account, the whitelisted configuration, and
// whether the desktop path must elevate through sudo.
func (d Deps) environment() (values map[string]string, elevate bool, ok bool) {
	values, err := d.Config()
	if err != nil {
		fmt.Fprintln(d.Stderr, "Invalid SSHDESK desktop account.")
		return nil, false, false
	}
	account, err := d.Account()
	if err != nil {
		fmt.Fprintf(d.Stderr, "sshdesk: could not determine the current account: %s\n", err)
		return nil, false, false
	}
	return values, values["RUN_AS"] != account, true
}

// applyEnv exports the resolved whitelist values for the child session,
// mirroring the original wrapper's export block on every route.
func (d Deps) applyEnv(values map[string]string) {
	for _, key := range config.Keys {
		if value := values[key]; value != "" {
			os.Setenv(key, value)
		}
	}
}

// shellRoute execs the authenticated account's own login shell. An empty
// command starts an interactive login shell; otherwise the command is passed
// verbatim through the shell's -c, exactly as sshd runs a remote command
// without a ForceCommand. It never elevates through RUN_AS/sudo.
func (d Deps) shellRoute(command string) int {
	shell, err := d.LoginShell()
	if err != nil {
		fmt.Fprintf(d.Stderr, "sshdesk: %s\n", err)
		return 1
	}
	return d.execOrReport(shellArgv(shell, command))
}

func (d Deps) execOrReport(argv []string) int {
	code, err := d.Exec(argv)
	if err != nil {
		fmt.Fprintf(d.Stderr, "sshdesk: %s\n", err)
		return 1
	}
	return code
}

// shellArgv builds the exec vector mirroring sshd: an interactive session is
// a login shell through a dash-prefixed argv[0]; a remote command runs
// through the plain shell with -c. cmd.exe/PowerShell have no login mode and
// take /c instead.
func shellArgv(shell, command string) []string {
	if isWindows {
		if command == "" {
			return []string{shell}
		}
		return []string{shell, "/c", command}
	}
	if command != "" {
		return []string{shell, "-c", command}
	}
	base := filepath.Base(shell)
	if !strings.HasPrefix(base, "-") {
		base = "-" + base
	}
	return []string{base}
}
