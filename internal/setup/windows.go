//go:build windows

package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// DefaultDeps wires the Windows installer to the real operating system.
func DefaultDeps(stdout, stderr io.Writer) Deps {
	d := Deps{
		Stdout:  stdout,
		Stderr:  stderr,
		Getenv:  os.Getenv,
		Getuid:  func() int { return 0 },
		IsAdmin: currentProcessElevated,
		LookupUser: func(name string) (string, int, int, error) {
			home := os.Getenv("USERPROFILE")
			if home == "" {
				return "", 0, 0, errors.New("USERPROFILE is not set")
			}
			return home, 0, 0, nil
		},
		CurrentUser: func() (string, error) {
			name := os.Getenv("USERNAME")
			if name == "" {
				return "", errors.New("USERNAME is not set")
			}
			return name, nil
		},
		Logname: func() (string, error) {
			return os.Getenv("USERNAME"), nil
		},
		SelfPath: os.Executable,
		LookPath: exec.LookPath,
		Chown:    func(string, int, int) error { return nil },
		Confirm: func(string) bool {
			fmt.Fprint(stdout, "Proceed? [y/N] ")
			line, err := bufio.NewReader(os.Stdin).ReadString('\n')
			if err != nil && line == "" {
				return false
			}
			answer := strings.ToLower(strings.TrimSpace(line))
			return answer == "y" || answer == "yes"
		},
	}
	d.Run = func(name string, args ...string) error {
		command := exec.Command(name, args...)
		command.Stdout = d.Stdout
		command.Stderr = d.Stderr
		return command.Run()
	}
	return d
}

// currentProcessElevated reports whether the process holds an elevated
// (Administrator) token.
func currentProcessElevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}

func install(d Deps, opts InstallOptions) int {
	return windowsInstall(d, opts)
}

func uninstall(d Deps, opts UninstallOptions) int {
	return windowsUninstall(d, opts)
}

// windowsInstall mirrors the former scripts/install.ps1 plus
// install-windows.ps1 flow: a single sshdesk.exe with .cmd wrappers, a PATH
// registry entry, and — with Administrator rights — the sshd_config marker
// block, firewall rule, and service startup.
func windowsInstall(d Deps, opts InstallOptions) int {
	account := opts.User
	if account == "" {
		account = d.Getenv("USERNAME")
	}
	if !accountPattern.MatchString(account) {
		d.errf("the Windows account name cannot be represented safely in sshd_config: %s", account)
		return 2
	}
	admin := d.IsAdmin()
	installRoot := filepath.Join(d.Getenv("LOCALAPPDATA"), "SSHDESK")
	if admin || d.Getenv("LOCALAPPDATA") == "" {
		if programData := d.Getenv("ProgramData"); programData != "" {
			installRoot = filepath.Join(programData, "SSHDESK")
		}
	}
	binDir := filepath.Join(installRoot, "bin")
	exePath := filepath.Join(binDir, "sshdesk.exe")

	var plan strings.Builder
	fmt.Fprintf(&plan, "SSHDESK install plan for account '%s' (Windows):\n", account)
	fmt.Fprintf(&plan, "  - install %s and %d .cmd wrappers in %s\n", exePath, len(CommandNames), binDir)
	fmt.Fprintf(&plan, "  - add %s to the %s PATH\n", binDir, map[bool]string{true: "system", false: "user"}[admin])
	if admin {
		plan.WriteString("  - add the forced-command block to sshd_config and start OpenSSH")
	} else {
		plan.WriteString("  - sshd_config and the OpenSSH service need Administrator rights; rerun elevated to configure them")
	}
	if !d.confirmPlan(plan.String(), opts.Yes) {
		return 0
	}

	self, err := d.SelfPath()
	if err != nil {
		d.errf("could not locate the running binary: %s", err)
		return 1
	}

	steps := []step{
		{"install-binary", func() error {
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				return err
			}
			if err := copySelfBinary(self, exePath); err != nil {
				return err
			}
			for _, name := range CommandNames {
				wrapper := filepath.Join(binDir, name+".cmd")
				if err := os.WriteFile(wrapper,
					[]byte(RenderWindowsWrapper(exePath, Subcommand(name))), 0o644); err != nil {
					return fmt.Errorf("could not write %s: %w", wrapper, err)
				}
			}
			return nil
		}},
		{"update-path", func() error {
			if err := registryAddPath(admin, binDir); err != nil {
				d.say(fmt.Sprintf("warning: could not update the PATH registry value: %s", err))
				d.say(fmt.Sprintf("         add %s to PATH manually", binDir))
			}
			return nil
		}},
	}
	if admin {
		steps = append(steps,
			step{"configure-sshd", func() error {
				return configureWindowsSshd(d, account, binDir)
			}},
			step{"start-openssh", func() error {
				return startWindowsOpenSSH(d)
			}},
		)
	}
	if err := d.runSteps(steps); err != nil {
		d.errf("%s", err)
		return 1
	}

	d.say("")
	d.say(fmt.Sprintf("Installed SSHDESK in %s.", installRoot))
	if admin {
		d.say(fmt.Sprintf("Desktop: ssh -t %s@<server-address> desktop (plain ssh starts a standard shell)", account))
	} else {
		d.say("To configure sshd and the OpenSSH service, rerun elevated: sshdesk --install")
	}
	d.say("warning: Windows OpenSSH normally runs in Session 0. Desktop capture from a forced")
	d.say("         command is experimental and must reach the logged-in interactive desktop.")
	d.say("Remove it with: sshdesk --uninstall")
	return 0
}

// windowsUninstall removes the marker block, the PATH entry, and the install
// tree. The OpenSSH capability and firewall rule are left untouched.
func windowsUninstall(d Deps, opts UninstallOptions) int {
	account := opts.User
	if account == "" {
		account = d.Getenv("USERNAME")
	}
	if !accountPattern.MatchString(account) {
		d.errf("invalid account name: %s", account)
		return 2
	}
	admin := d.IsAdmin()
	installRoot := filepath.Join(d.Getenv("LOCALAPPDATA"), "SSHDESK")
	if admin || d.Getenv("LOCALAPPDATA") == "" {
		if programData := d.Getenv("ProgramData"); programData != "" {
			installRoot = filepath.Join(programData, "SSHDESK")
		}
	}

	var plan strings.Builder
	fmt.Fprintf(&plan, "SSHDESK uninstall plan for account '%s' (Windows):\n", account)
	if admin {
		plan.WriteString("  - remove the sshd_config marker block and restart OpenSSH\n")
	}
	fmt.Fprintf(&plan, "  - remove the PATH entry and %s\\", installRoot)
	if !d.confirmPlan(plan.String(), opts.Yes) {
		return 0
	}

	steps := []step{}
	if admin {
		steps = append(steps, step{"remove-sshd-block", func() error {
			sshdConfig, sshd, ok := windowsSshdPaths(d)
			if !ok {
				return nil
			}
			content, err := os.ReadFile(sshdConfig)
			if err != nil {
				return nil
			}
			updated := RemoveMarkedBlock(string(content), account)
			if updated == string(content) {
				return nil
			}
			if err := os.WriteFile(sshdConfig, []byte(updated), 0o644); err != nil {
				return err
			}
			if err := d.Run(sshd, "-t", "-f", sshdConfig); err != nil {
				os.WriteFile(sshdConfig, content, 0o644)
				return errors.New("OpenSSH rejected the configuration after removing the block; it was rolled back")
			}
			d.Run("powershell", "-NoProfile", "-Command",
				"if ((Get-Service sshd).Status -eq 'Running') { Restart-Service sshd }")
			d.say("Removed the sshd_config marker block.")
			return nil
		}})
	}
	steps = append(steps,
		step{"remove-path", func() error {
			if err := registryRemovePath(admin, filepath.Join(installRoot, "bin")); err != nil {
				d.say(fmt.Sprintf("warning: could not update the PATH registry value: %s", err))
			}
			return nil
		}},
		step{"remove-files", func() error {
			if !fileExists(installRoot) {
				d.say(fmt.Sprintf("No install tree at %s; skipping.", installRoot))
				return nil
			}
			if err := os.RemoveAll(installRoot); err != nil {
				return err
			}
			d.say(fmt.Sprintf("Removed %s.", installRoot))
			return nil
		}},
	)
	if err := d.runSteps(steps); err != nil {
		d.errf("%s", err)
		return 1
	}
	d.say("Uninstall complete.")
	return 0
}

// windowsSshdPaths locates the Windows OpenSSH configuration and binary,
// warning when the OpenSSH Server capability is missing.
func windowsSshdPaths(d Deps) (sshdConfig, sshd string, ok bool) {
	sshdConfig = filepath.Join(d.Getenv("ProgramData"), "ssh", "sshd_config")
	sshd = filepath.Join(d.Getenv("SystemRoot"), "System32", "OpenSSH", "sshd.exe")
	if !fileExists(sshd) || !fileExists(sshdConfig) {
		d.say("warning: the Windows OpenSSH Server capability is not installed;")
		d.say("         install it under Settings > Apps > Optional Features, then rerun")
		return "", "", false
	}
	return sshdConfig, sshd, true
}

// configureWindowsSshd replaces the per-account marker block and validates
// the result, rolling back on a rejected configuration.
func configureWindowsSshd(d Deps, account, binDir string) error {
	sshdConfig, sshd, ok := windowsSshdPaths(d)
	if !ok {
		return nil
	}
	content, err := os.ReadFile(sshdConfig)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", sshdConfig, err)
	}
	forcedCommand := filepath.ToSlash(filepath.Join(binDir, "sshdesk-forced-command.cmd"))
	updated := ReplaceMarkedBlock(string(content), account, forcedCommand)
	backup := sshdConfig + ".before-sshdesk"
	if err := os.WriteFile(backup, content, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(sshdConfig, []byte(updated), 0o644); err != nil {
		return err
	}
	if err := d.Run(sshd, "-t", "-f", sshdConfig); err != nil {
		os.WriteFile(sshdConfig, content, 0o644)
		return errors.New("OpenSSH rejected the SSHDESK configuration; it was rolled back")
	}
	return nil
}

// startWindowsOpenSSH sets automatic startup, ensures the inbound firewall
// rule, and starts or restarts the sshd service.
func startWindowsOpenSSH(d Deps) error {
	if err := d.Run("sc.exe", "config", "sshd", "start=", "auto"); err != nil {
		return fmt.Errorf("could not set the sshd service to automatic: %w", err)
	}
	if err := d.Run("netsh", "advfirewall", "firewall", "show", "rule",
		"name=OpenSSH-Server-In-TCP"); err != nil {
		if err := d.Run("netsh", "advfirewall", "firewall", "add", "rule",
			"name=OpenSSH-Server-In-TCP", "dir=in", "action=allow",
			"protocol=TCP", "localport=22"); err != nil {
			return fmt.Errorf("could not add the OpenSSH firewall rule: %w", err)
		}
	}
	if err := d.Run("powershell", "-NoProfile", "-Command",
		"if ((Get-Service sshd).Status -eq 'Running') { Restart-Service sshd } else { Start-Service sshd }"); err != nil {
		return fmt.Errorf("could not start the sshd service: %w", err)
	}
	return nil
}

// registryPathKey opens the Environment key holding the system (HKLM) or
// user (HKCU) PATH.
func registryPathKey(system bool) (registry.Key, error) {
	if system {
		return registry.OpenKey(registry.LOCAL_MACHINE,
			`SYSTEM\CurrentControlSet\Control\Session Manager\Environment`,
			registry.QUERY_VALUE|registry.SET_VALUE)
	}
	return registry.OpenKey(registry.CURRENT_USER, `Environment`,
		registry.QUERY_VALUE|registry.SET_VALUE)
}

func registryReadPath(key registry.Key) string {
	value, _, err := key.GetStringValue("Path")
	if err != nil {
		return ""
	}
	return value
}

func registryAddPath(system bool, entry string) error {
	key, err := registryPathKey(system)
	if err != nil {
		return err
	}
	defer key.Close()
	updated := AddPathEntry(registryReadPath(key), entry)
	return key.SetExpandStringValue("Path", updated)
}

func registryRemovePath(system bool, entry string) error {
	key, err := registryPathKey(system)
	if err != nil {
		return err
	}
	defer key.Close()
	updated := RemovePathEntry(registryReadPath(key), entry)
	return key.SetExpandStringValue("Path", updated)
}
