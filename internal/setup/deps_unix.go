//go:build linux || darwin

package setup

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// unixDeps wires every Deps seam to the real operating system.
func unixDeps(stdout, stderr io.Writer) Deps {
	d := Deps{
		Stdout: stdout,
		Stderr: stderr,
		Getenv: os.Getenv,
		Getuid: os.Getuid,
		IsAdmin: func() bool {
			return os.Getuid() == 0
		},
		LookupUser: func(name string) (string, int, int, error) {
			account, err := user.Lookup(name)
			if err != nil {
				return "", 0, 0, err
			}
			uid, err := strconv.Atoi(account.Uid)
			if err != nil {
				return "", 0, 0, err
			}
			gid, err := strconv.Atoi(account.Gid)
			if err != nil {
				return "", 0, 0, err
			}
			return account.HomeDir, uid, gid, nil
		},
		CurrentUser: func() (string, error) {
			current, err := user.Current()
			if err != nil {
				return "", err
			}
			return current.Username, nil
		},
		Logname: func() (string, error) {
			output, err := exec.Command("logname").Output()
			if err != nil {
				return "", err
			}
			return strings.TrimSpace(string(output)), nil
		},
		SelfPath: func() (string, error) {
			path, err := os.Executable()
			if err != nil {
				return "", err
			}
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				return resolved, nil
			}
			return path, nil
		},
		LookPath: exec.LookPath,
		Chown:    os.Chown,
		Confirm:  confirmOnTerminal(stdout),
	}
	d.Run = func(name string, args ...string) error {
		command := exec.Command(name, args...)
		command.Stdout = d.Stdout
		command.Stderr = d.Stderr
		return command.Run()
	}
	return d
}

// confirmOnTerminal asks on /dev/tty when possible so the prompt works even
// through sudo with redirected standard streams; stdin is the fallback.
func confirmOnTerminal(stdout io.Writer) func(plan string) bool {
	return func(string) bool {
		var reader *bufio.Reader
		if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			defer tty.Close()
			fmt.Fprint(tty, "Proceed? [y/N] ")
			reader = bufio.NewReader(tty)
		} else {
			fmt.Fprint(stdout, "Proceed? [y/N] ")
			reader = bufio.NewReader(os.Stdin)
		}
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return false
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		return answer == "y" || answer == "yes"
	}
}
