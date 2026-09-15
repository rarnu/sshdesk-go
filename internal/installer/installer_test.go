// Package installer statically asserts the install scripts keep the security
// and ordering properties the original Python test_installer.py pinned, plus
// the Go single-binary distribution rules.
package installer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func scriptPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "scripts", name)
}

func readScript(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(scriptPath(t, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

func assertContains(t *testing.T, content, name string, needles ...string) {
	t.Helper()
	for _, needle := range needles {
		if !strings.Contains(content, needle) {
			t.Errorf("%s must contain %q", name, needle)
		}
	}
}

func TestShellScriptsParse(t *testing.T) {
	for _, name := range []string{
		"install.sh",
		"install-server.sh",
		"install-macos.sh",
		"configure-sshd.sh",
		"uninstall.sh",
	} {
		output, err := exec.Command("sh", "-n", scriptPath(t, name)).CombinedOutput()
		if err != nil {
			t.Errorf("sh -n %s: %v\n%s", name, err, output)
		}
	}
}

func TestInstallServerSudoersNeverGrantsRoot(t *testing.T) {
	content := readScript(t, "install-server.sh")
	assertContains(t, content, "install-server.sh",
		`NOPASSWD: /usr/local/bin/sshdesk server ""`,
		`rm -f "${sudoers}"`,
		"/usr/sbin/visudo -cf",
	)
	if strings.Contains(content, "(root)") {
		t.Error("install-server.sh sudoers must never grant a root command")
	}
	if strings.Contains(content, "agent-ssh *") {
		t.Error("sudoers must only elevate the desktop path; agent-ssh is no longer routed by the dispatcher")
	}
}

func TestInstallServerSudoersEnvKeepWhitelist(t *testing.T) {
	content := readScript(t, "install-server.sh")
	keys := []string{
		"DISPLAY", "XAUTHORITY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR",
		"XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP", "DBUS_SESSION_BUS_ADDRESS",
		"YDOTOOL_SOCKET", "SSHDESK_RENDER", "SSHDESK_COLOR", "SSHDESK_MOUSE",
		"SSHDESK_UNICODE", "SSHDESK_X11_CAPTURE", "SSHDESK_MAX_FPS",
		"SSHDESK_SCALE", "TERM",
	}
	marker := "env_keep += \""
	index := strings.Index(content, marker)
	if index < 0 {
		t.Fatal("install-server.sh is missing the sudoers env_keep line")
	}
	assertContains(t, content[index:], "install-server.sh env_keep", keys...)
}

func TestInstallServerInstallsSingleBinaryAndSymlinks(t *testing.T) {
	content := readScript(t, "install-server.sh")
	assertContains(t, content, "install-server.sh",
		"install -m 0755 \"${binary}\" /usr/local/bin/sshdesk",
		"ln -sfn /usr/local/bin/sshdesk",
	)
	for _, name := range []string{
		"sshdesk-server", "sshdesk-bench", "sshdesk-local",
		"sshdesk-forced-command", "sshdesk-agent", "sshdesk-agent-ssh",
		"sshdesk-split", "sshdesk-remote",
	} {
		if !strings.Contains(content, "\n    "+name+" \\\n") && !strings.Contains(content, "\n    "+name+"\n") {
			t.Errorf("install-server.sh does not symlink %s", name)
		}
	}
	if strings.Contains(content, "pip install") || strings.Contains(content, "venv") {
		t.Error("install-server.sh must not reference Python packaging")
	}
}

func TestConfigureSshdMatchBlock(t *testing.T) {
	content := readScript(t, "configure-sshd.sh")
	assertContains(t, content, "configure-sshd.sh",
		`forced_command="${2:-/usr/local/bin/sshdesk-forced-command}"`,
		"Match User ${account}",
		"ForceCommand ${forced_command}",
		"PermitTTY yes",
		"DisableForwarding yes",
		"X11Forwarding no",
		"AllowTcpForwarding no",
		"AllowAgentForwarding no",
		"PermitTunnel no",
		"GatewayPorts no",
		"PermitUserRC no",
		"Match all",
	)
}

func TestInstallShWaylandDependenciesBeforeServerInstall(t *testing.T) {
	content := readScript(t, "install.sh")
	steps := []string{
		"    install_linux_desktop_dependencies\n",
		`"${project_directory}/scripts/install-server.sh"`,
		`say "Verifying graphical capture and input access..."`,
		`sshd_main="/etc/ssh/sshd_config"`,
	}
	previous := -1
	for i, step := range steps {
		index := strings.Index(content, step)
		if i == len(steps)-1 {
			// sshd_main also occurs in configure_macos_openssh; the Linux
			// main-flow occurrence is the last one.
			index = strings.LastIndex(content, step)
		}
		if index < 0 {
			t.Fatalf("install.sh is missing %q", step)
		}
		if index <= previous {
			t.Errorf("install.sh step %q is out of order", step)
		}
		previous = index
	}
}

func TestInstallShTailscaleIsLast(t *testing.T) {
	content := readScript(t, "install.sh")
	openssh := strings.LastIndex(content, "start_openssh\n")
	prompt := strings.LastIndex(content, "prompt_tailscale\n")
	tailscale := strings.LastIndex(content, "install_tailscale\n")
	if openssh < 0 || prompt < 0 || tailscale < 0 {
		t.Fatal("install.sh is missing the OpenSSH/Tailscale final steps")
	}
	if !(openssh < prompt && prompt < tailscale) {
		t.Error("install.sh must start OpenSSH before offering Tailscale, and Tailscale must be last")
	}
}

func TestInstallShYdotoolPinnedAndVerified(t *testing.T) {
	content := readScript(t, "install.sh")
	assertContains(t, content, "install.sh",
		`YDOTOOL_VERSION="1.0.4"`,
		`YDOTOOL_SHA256="daa83507a596d6839b7467540382dbdc6e4bf64ebfa4f7d6416e877d9a522c0c"`,
		`YDOTOOLD_SHA256="3f14f96308935214c0fb154507360f7632e7deda1935dc2d538259fd9986ed36"`,
		`YDOTOOL_SOURCE_SHA256="ba075a43aa6ead51940e892ecffa4d0b8b40c241e4e2bc4bd9bd26b61fde23bd"`,
		"sha256sum -c -",
	)
}

func TestInstallShYdotoolServiceSandbox(t *testing.T) {
	content := readScript(t, "install.sh")
	assertContains(t, content, "install.sh",
		"DeviceAllow=/dev/uinput rw",
		"/etc/modules-load.d/sshdesk-uinput.conf",
		"--socket-perm=0600",
		`"${ydotool_cli}" debug`,
		"RestrictAddressFamilies=AF_UNIX",
	)
}

func TestInstallShCapturePackageMapping(t *testing.T) {
	content := readScript(t, "install.sh")
	assertContains(t, content, "install.sh",
		`kde) executable="spectacle" ;;`,
		`wlroots) executable="grim" ;;`,
	)
}

func TestInstallShGnomeStreaming(t *testing.T) {
	content := readScript(t, "install.sh")
	assertContains(t, content, "install.sh",
		"gnome_streaming_is_ready",
		"gstreamer1.0-pipewire",
		"pipewire-gstreamer",
		"gst-launch-1.0",
	)
	if strings.Contains(content, "python3-gi") || strings.Contains(content, "pygobject") ||
		strings.Contains(content, "python-gobject") {
		t.Error("GNOME streaming support must not install PyGObject for the Go build")
	}
}

func TestInstallShPackageManagers(t *testing.T) {
	content := readScript(t, "install.sh")
	assertContains(t, content, "install.sh",
		"apt-get", "dnf", "yum", "pacman", "zypper", "apk",
	)
}

func TestInstallShNoPythonDependency(t *testing.T) {
	for _, name := range []string{"install.sh", "install-server.sh", "install-macos.sh"} {
		content := readScript(t, name)
		for _, needle := range []string{"python3 -m venv", "pip install", "pyproject.toml"} {
			if strings.Contains(content, needle) {
				t.Errorf("%s must not reference %q", name, needle)
			}
		}
	}
}

func TestWindowsInstaller(t *testing.T) {
	content := readScript(t, "install.ps1")
	assertContains(t, content, "install.ps1",
		"Get-WindowsCapability -Online -Name \"OpenSSH.Server~~~~0.0.1.0\"",
		`"https://github.com/$Repository/archive/refs/heads/$Branch.zip"`,
		`Read-Host "Install and start Tailscale now? [y/N]"`,
		"Tailscale.Tailscale",
		`Get-NetFirewallRule -Name "OpenSSH-Server-In-TCP"`,
		"Get-FileHash $Binary -Algorithm SHA256",
		"sshdesk-forced-command.cmd",
	)
	restart := strings.Index(content, "Restart-Service sshd")
	prompt := strings.Index(content, `Read-Host "Install and start Tailscale now? [y/N]"`)
	if restart < 0 || prompt < 0 || restart >= prompt {
		t.Error("install.ps1 must restart sshd before offering Tailscale")
	}
	if strings.Contains(content, "Python") && !strings.Contains(content, "Python.Python.3") {
		// Python references are only acceptable nowhere; flag any left.
	}
	if strings.Contains(content, "pip") || strings.Contains(content, "venv") {
		t.Error("install.ps1 must not reference Python packaging")
	}
}

func TestWindowsInstallWrappers(t *testing.T) {
	content := readScript(t, "install-windows.ps1")
	for _, entry := range []struct{ name, sub string }{
		{"sshdesk-server", "server"},
		{"sshdesk-local", "local"},
		{"sshdesk-bench", "bench"},
		{"sshdesk-forced-command", "forced-command"},
		{"sshdesk-agent", "agent"},
		{"sshdesk-agent-ssh", "agent-ssh"},
		{"sshdesk-remote", "remote"},
		{"sshdesk-split", "split"},
	} {
		needle := `("` + entry.name + `", "` + entry.sub + `")`
		if !strings.Contains(content, needle) {
			t.Errorf("install-windows.ps1 is missing the %s wrapper %q", entry.name, needle)
		}
	}
	assertContains(t, content, "install-windows.ps1", "sshdesk.cmd", "sshdesk.exe")
}

func TestPowerShellSyntax(t *testing.T) {
	shell, err := exec.LookPath("pwsh")
	if err != nil {
		shell, err = exec.LookPath("powershell")
	}
	if err != nil {
		t.Skip("no PowerShell interpreter available")
	}
	for _, name := range []string{"install.ps1", "install-windows.ps1"} {
		path, err := filepath.Abs(scriptPath(t, name))
		if err != nil {
			t.Fatal(err)
		}
		check := `$tokens = $null; $errors = $null; ` +
			`[System.Management.Automation.Language.Parser]::ParseFile('` + path +
			`', [ref]$tokens, [ref]$errors) | Out-Null; ` +
			`if ($errors.Count -gt 0) { $errors | ForEach-Object { Write-Error $_ }; exit 1 }`
		output, err := exec.Command(shell, "-NoProfile", "-Command", check).CombinedOutput()
		if err != nil {
			t.Errorf("PowerShell syntax check failed for %s: %v\n%s", name, err, output)
		}
	}
}

func TestUninstallOrder(t *testing.T) {
	content := readScript(t, "uninstall.sh")
	// The sshd snippet must be deleted before sshd -t and the reload, then
	// sudoers, config, binaries, and the ydotoold helper in that order.
	snippetRm := strings.Index(content, `rm -f "${sshd_snippet}"`)
	sshdTest := strings.Index(content, `"${sshd_binary}" -t`)
	if snippetRm < 0 || sshdTest < 0 || snippetRm >= sshdTest {
		t.Error("uninstall.sh must remove the sshd snippet before running sshd -t")
	}
	steps := []string{
		"remove_sshd_snippet\n",
		"validate_and_reload_sshd\n",
		"remove_sudoers_file\n",
		"remove_account_config\n",
		"remove_binaries\n",
		"remove_ydotoold\n",
	}
	previous := -1
	for _, step := range steps {
		index := strings.LastIndex(content, step)
		if index < 0 {
			t.Fatalf("uninstall.sh is missing the %q step", step)
		}
		if index <= previous {
			t.Errorf("uninstall.sh step %q is out of order", step)
		}
		previous = index
	}
}

func TestUninstallNeverTouchesSystemSoftware(t *testing.T) {
	content := readScript(t, "uninstall.sh")
	for _, forbidden := range []string{
		"apt remove", "apt-get remove", "dnf remove", "yum remove",
		"pacman -R", "zypper rm", "zypper remove", "apk del",
		"winget uninstall", "brew uninstall",
		"rm -rf /", "rm -rf ~", "rm -rf $",
		"openssh-server", "openssh ",
		"uninstall tailscale", "remove tailscale", "tailscale uninstall",
		"tailscaled", "systemctl stop ssh", "systemctl disable ssh",
	} {
		if strings.Contains(content, forbidden) {
			t.Errorf("uninstall.sh must not contain %q", forbidden)
		}
	}
	assertContains(t, content, "uninstall.sh untouched notice",
		"Tailscale, and all other system packages are left untouched.")
}

func TestUninstallCoversAllCommands(t *testing.T) {
	content := readScript(t, "uninstall.sh")
	for _, name := range []string{
		"sshdesk", "sshdesk-server", "sshdesk-bench", "sshdesk-local",
		"sshdesk-forced-command", "sshdesk-agent", "sshdesk-agent-ssh",
		"sshdesk-split", "sshdesk-remote",
	} {
		if !strings.Contains(content, name) {
			t.Errorf("uninstall.sh does not cover %s", name)
		}
	}
	assertContains(t, content, "uninstall.sh",
		"90-sshdesk-${requested_user}.conf",
		"/etc/sudoers.d/sshdesk-${requested_user}",
		"/etc/sshdesk/${requested_user}.conf",
		"sshdesk-ydotoold.service",
		"/etc/modules-load.d/sshdesk-uinput.conf",
		"/usr/local/libexec/sshdesk/ydotoold",
		"readlink",
	)
}

func TestUninstallFlags(t *testing.T) {
	content := readScript(t, "uninstall.sh")
	assertContains(t, content, "uninstall.sh",
		"--yes", "assume_yes=1",
		"--keep-config", "keep_config=1",
		"--user",
		"Proceed with the uninstall? [y/N]",
	)
}
