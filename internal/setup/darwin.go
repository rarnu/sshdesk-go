package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// darwinPaths locates every path the macOS installer touches. The
// application itself is always user-level; the sshd snippet is system-wide
// and only written when running as root.
type darwinPaths struct {
	home          string
	installRoot   string
	binDir        string
	sshdConfig    string
	sshdConfigDir string
}

func defaultDarwinPaths(home string) darwinPaths {
	return darwinPaths{
		home:          home,
		installRoot:   filepath.Join(home, ".local", "share", "sshdesk"),
		binDir:        filepath.Join(home, ".local", "bin"),
		sshdConfig:    "/etc/ssh/sshd_config",
		sshdConfigDir: "/etc/ssh/sshd_config.d",
	}
}

func (p darwinPaths) binPath() string { return filepath.Join(p.installRoot, "sshdesk") }
func (p darwinPaths) snippet(account string) string {
	return filepath.Join(p.sshdConfigDir, "90-sshdesk-"+account+".conf")
}

// darwinInstall mirrors the former scripts/install.sh plus install-macos.sh
// macOS flow: a user-level install under ~/.local, plus the system-wide sshd
// snippet and Remote Login when run with sudo.
func darwinInstall(d Deps, opts InstallOptions, p darwinPaths) int {
	account, code := resolveAccount(d, opts.User)
	if code != 0 {
		return code
	}
	if _, _, _, err := validateAccount(d, account); err != nil {
		d.errf("%s", err)
		return 1
	}
	if d.Getuid() != 0 {
		current, err := d.CurrentUser()
		if err != nil || current != account {
			d.errf("run the macOS installer while logged in as %s", account)
			return 1
		}
	}

	rooted := d.Getuid() == 0
	var plan strings.Builder
	fmt.Fprintf(&plan, "SSHDESK install plan for account '%s' (macOS):\n", account)
	fmt.Fprintf(&plan, "  - install %s and %d command symlinks in %s\n",
		p.binPath(), len(CommandNames), p.binDir)
	if rooted {
		fmt.Fprintf(&plan, "  - add the forced-command snippet %s and enable Remote Login\n",
			p.snippet(account))
	} else {
		plan.WriteString("  - the sshd snippet and Remote Login need sudo; rerun with sudo to configure them")
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
			if err := os.MkdirAll(p.installRoot, 0o755); err != nil {
				return err
			}
			if err := os.MkdirAll(p.binDir, 0o755); err != nil {
				return err
			}
			if err := copySelfBinary(self, p.binPath()); err != nil {
				return err
			}
			for _, name := range CommandNames {
				link := filepath.Join(p.binDir, name)
				if info, err := os.Lstat(link); err == nil {
					if info.Mode()&os.ModeSymlink == 0 {
						return fmt.Errorf("%s exists and is not a symlink; refusing to overwrite it", link)
					}
					if err := os.Remove(link); err != nil {
						return err
					}
				}
				if err := os.Symlink(p.binPath(), link); err != nil {
					return fmt.Errorf("could not create %s: %w", link, err)
				}
			}
			return nil
		}},
	}
	if rooted {
		steps = append(steps, step{"configure-sshd", func() error {
			forcedCommand := filepath.Join(p.binDir, "sshdesk-forced-command")
			return configureUnixSshd(d, p.sshdConfig, p.sshdConfigDir,
				p.snippet(account), RenderSshdSnippet(account, forcedCommand),
				"Include /etc/ssh/sshd_config.d/*", 0o644)
		}}, step{"enable-remote-login", func() error {
			if err := d.Run("/usr/sbin/systemsetup", "-setremotelogin", "on"); err != nil {
				d.say("warning: macOS could not enable Remote Login; grant Terminal Full Disk Access,")
				d.say("         enable it in System Settings > General > Sharing, or rerun with sudo")
			}
			return nil
		}})
	}
	if err := d.runSteps(steps); err != nil {
		d.errf("%s", err)
		return 1
	}

	d.say("")
	d.say(fmt.Sprintf("Installed SSHDESK in %s.", p.installRoot))
	if rooted {
		d.say("SSHDESK is installed and macOS Remote Login is running.")
		d.say(fmt.Sprintf("Desktop: ssh -t %s@<server-address> desktop", account))
		d.say(fmt.Sprintf("Plain ssh %s@<server-address> starts a standard shell.", account))
	} else {
		d.say(fmt.Sprintf("Add %s to PATH.", p.binDir))
		d.say("To configure sshd and Remote Login, rerun with: sudo sshdesk --install")
	}
	d.say("Grant Screen Recording and Accessibility permission to the sshdesk binary")
	d.say("in System Settings > Privacy & Security before connecting.")
	d.say("Remove it with: sshdesk --uninstall")
	return 0
}

// darwinUninstall removes the user-level install and, when run with sudo,
// the system-wide sshd snippet. Foreign files in ~/.local/bin are kept.
func darwinUninstall(d Deps, opts UninstallOptions, p darwinPaths) int {
	account, code := resolveAccount(d, opts.User)
	if code != 0 {
		return code
	}
	rooted := d.Getuid() == 0
	if !rooted {
		current, err := d.CurrentUser()
		if err != nil || current != account {
			d.errf("run the macOS uninstaller while logged in as %s", account)
			return 1
		}
	}

	var plan strings.Builder
	fmt.Fprintf(&plan, "SSHDESK uninstall plan for account '%s' (macOS):\n", account)
	if fileExists(p.snippet(account)) {
		if rooted {
			fmt.Fprintf(&plan, "  - remove %s\n", p.snippet(account))
		} else {
			fmt.Fprintf(&plan, "  - keep %s (needs sudo; rerun with sudo)\n", p.snippet(account))
		}
	}
	for _, name := range CommandNames {
		if fileExists(filepath.Join(p.binDir, name)) {
			fmt.Fprintf(&plan, "  - remove %s\n", filepath.Join(p.binDir, name))
		}
	}
	if fileExists(p.installRoot) {
		fmt.Fprintf(&plan, "  - remove %s/\n", p.installRoot)
	}
	plan.WriteString("Remote Login and the sshd_config Include line are left untouched.")
	if !d.confirmPlan(plan.String(), opts.Yes) {
		return 0
	}

	steps := []step{
		{"remove-sshd-snippet", func() error {
			target := p.snippet(account)
			if !fileExists(target) {
				return nil
			}
			if !rooted {
				d.say(fmt.Sprintf("note: %s needs Administrator (sudo) rights to remove; leaving it", target))
				return nil
			}
			if err := os.Remove(target); err != nil {
				return err
			}
			d.say(fmt.Sprintf("Removed %s.", target))
			sshd, err := findSshd(d)
			if err != nil {
				return err
			}
			if err := d.Run(sshd, "-t"); err != nil {
				return fmt.Errorf("sshd -t failed after removing the snippet; fix sshd_config and rerun")
			}
			return nil
		}},
		{"remove-binaries", func() error {
			removeOwnedCommands(d, p.binDir, p.binPath())
			if fileExists(p.installRoot) {
				if err := os.RemoveAll(p.installRoot); err != nil {
					return err
				}
				d.say(fmt.Sprintf("Removed %s.", p.installRoot))
			}
			return nil
		}},
	}
	if err := d.runSteps(steps); err != nil {
		d.errf("%s", err)
		return 1
	}
	d.say("Uninstall complete.")
	return 0
}
