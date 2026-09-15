//go:build windows

package forcedcmd

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"

	"golang.org/x/term"

	"github.com/rarnu/sshdesk-go/internal/config"
)

const isWindows = true

func defaultHasTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// defaultExec has no execve on Windows; it runs the vector as a child with
// the console attached and reports the child's exit code.
func defaultExec(argv []string) (int, error) {
	command := exec.Command(argv[0], argv[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return exitError.ExitCode(), nil
		}
		return 1, err
	}
	return 0, nil
}

// defaultLoginShell uses COMSPEC (default cmd.exe); there is no login mode.
func defaultLoginShell() (string, error) {
	configured := os.Getenv("COMSPEC")
	if configured == "" {
		configured = "cmd.exe"
	}
	shell, err := exec.LookPath(configured)
	if err != nil {
		return "", fmt.Errorf("login shell is unavailable: %s", configured)
	}
	return shell, nil
}

func currentAccount() (string, error) {
	current, err := user.Current()
	if err == nil && current.Username != "" {
		return current.Username, nil
	}
	if name := os.Getenv("USERNAME"); name != "" {
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
