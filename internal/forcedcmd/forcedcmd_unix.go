//go:build !windows

package forcedcmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"syscall"

	"golang.org/x/term"

	"github.com/rarnu/sshdesk-go/internal/config"
)

const isWindows = false

func defaultHasTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// defaultExec replaces the process image; it only returns on failure. A
// dash-prefixed argv[0] (login shell) is stripped for the lookup while the
// original vector is preserved for the new process.
func defaultExec(argv []string) (int, error) {
	path, err := exec.LookPath(strings.TrimPrefix(argv[0], "-"))
	if err != nil {
		return 1, err
	}
	if err := syscall.Exec(path, argv, os.Environ()); err != nil {
		return 1, err
	}
	return 0, nil
}

// defaultLoginShell resolves the account's login shell from /etc/passwd,
// falling back to $SHELL and then /bin/sh, mirroring the Python _login_shell.
func defaultLoginShell() (string, error) {
	configured := passwdShell()
	if configured == "" {
		configured = os.Getenv("SHELL")
	}
	if configured == "" {
		configured = "/bin/sh"
	}
	shell := lookExecutable(configured)
	if shell == "" {
		return "", fmt.Errorf("login shell is unavailable: %s", configured)
	}
	return shell, nil
}

// passwdShell finds the current uid's shell in /etc/passwd. Directory-service
// accounts (macOS) fall through to the $SHELL fallback.
func passwdShell() string {
	current, err := user.Current()
	if err != nil {
		return ""
	}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) < 7 {
			continue
		}
		if fields[0] == current.Username || fields[2] == current.Uid {
			return fields[6]
		}
	}
	return ""
}

// lookExecutable mirrors shutil.which for one candidate.
func lookExecutable(name string) string {
	if strings.ContainsRune(name, '/') {
		if executable(name) {
			return name
		}
		return ""
	}
	found, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return found
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func currentAccount() (string, error) {
	current, err := user.Current()
	if err == nil && current.Username != "" {
		return current.Username, nil
	}
	if name := os.Getenv("USER"); name != "" {
		return name, nil
	}
	if err != nil {
		return "", err
	}
	return "", fmt.Errorf("could not determine the current account")
}

// DefaultDeps wires the real process side effects.
func DefaultDeps() Deps {
	return Deps{
		Getenv:      os.Getenv,
		Account:     currentAccount,
		Config:      config.Values,
		HasTerminal: defaultHasTerminal,
		Exec:        defaultExec,
		LoginShell:  defaultLoginShell,
		SelfPath:    os.Executable,
		Stderr:      os.Stderr,
	}
}
