package setup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// linuxPaths locates every system path the Linux installer touches. Tests
// point them at a temporary root.
type linuxPaths struct {
	binDir         string
	configDir      string
	sudoersDir     string
	sshdConfig     string
	sshdConfigDir  string
	systemdDir     string
	modulesLoadDir string
	libexecDir     string
	ydotoolCLI     string
}

func defaultLinuxPaths() linuxPaths {
	return linuxPaths{
		binDir:         "/usr/local/bin",
		configDir:      "/etc/sshdesk",
		sudoersDir:     "/etc/sudoers.d",
		sshdConfig:     "/etc/ssh/sshd_config",
		sshdConfigDir:  "/etc/ssh/sshd_config.d",
		systemdDir:     "/etc/systemd/system",
		modulesLoadDir: "/etc/modules-load.d",
		libexecDir:     "/usr/local/libexec/sshdesk",
		ydotoolCLI:     "/usr/local/bin/ydotool",
	}
}

func (p linuxPaths) binPath() string { return filepath.Join(p.binDir, "sshdesk") }
func (p linuxPaths) snippet(account string) string {
	return filepath.Join(p.sshdConfigDir, "90-sshdesk-"+account+".conf")
}
func (p linuxPaths) sudoers(account string) string {
	return filepath.Join(p.sudoersDir, "sshdesk-"+account)
}
func (p linuxPaths) accountConfig(account string) string {
	return filepath.Join(p.configDir, account+".conf")
}

// linuxInstall mirrors the former scripts/install.sh plus install-server.sh
// Linux flow: root-only, per-account config, sudoers, sshd snippet, Wayland
// helper. It never installs packages or downloads anything; missing capture
// or input dependencies produce warnings with package suggestions.
func linuxInstall(d Deps, opts InstallOptions, p linuxPaths) int {
	if d.Getuid() != 0 {
		d.errf("run the installer as root, for example: sudo sshdesk --install")
		return 1
	}
	account, code := resolveAccount(d, opts.User)
	if code != 0 {
		return code
	}
	home, uid, gid, err := validateAccount(d, account)
	if err != nil {
		d.errf("%s", err)
		if strings.HasPrefix(err.Error(), "invalid account name") {
			return 2
		}
		return 1
	}
	runAs := opts.RunAs
	if runAs == "" {
		runAs = account
	}
	if !accountPattern.MatchString(runAs) {
		d.errf("invalid account name: %s", runAs)
		return 2
	}
	if _, _, _, err := d.LookupUser(runAs); err != nil {
		d.errf("desktop user does not exist: %s", runAs)
		return 1
	}

	// Graphical session variables resolve in priority order: explicit flag,
	// the installer's own process environment, the environment harvested from
	// the account's running graphical session, then the historical defaults.
	var harvested map[string]string
	if d.HarvestSession != nil {
		harvested = d.HarvestSession(uid)
	}
	envValue := func(key string) string {
		if value := d.Getenv(key); value != "" {
			return value
		}
		return harvested[key]
	}
	if harvested["WAYLAND_DISPLAY"] != "" ||
		strings.EqualFold(harvested["XDG_SESSION_TYPE"], "wayland") {
		desktop := harvested["XDG_CURRENT_DESKTOP"]
		if desktop == "" {
			desktop = "unknown desktop"
		}
		sessionType := harvested["XDG_SESSION_TYPE"]
		if sessionType == "" || strings.EqualFold(sessionType, "wayland") {
			sessionType = "Wayland"
		}
		d.say(fmt.Sprintf("Detected a %s %s session for user %s; recorded its graphical environment.",
			desktop, sessionType, account))
	}

	// A session counts as detected when any source shows a live graphical
	// session: an explicit flag, a process DISPLAY/WAYLAND_DISPLAY, or a
	// harvested session. Headless installs still get a working X11-default
	// config, but skip the access check and end with a prominent warning.
	sessionDetected := opts.Display != "" ||
		envValue("DISPLAY") != "" ||
		envValue("WAYLAND_DISPLAY") != "" ||
		strings.EqualFold(envValue("XDG_SESSION_TYPE"), "wayland")

	display := opts.Display
	if display == "" {
		display = envValue("DISPLAY")
	}
	if display == "" && envValue("WAYLAND_DISPLAY") == "" {
		display = ":0"
	}
	xauthority := opts.XAuthority
	if xauthority == "" {
		xauthority = envValue("XAUTHORITY")
	}
	if xauthority == "" {
		runtimeDir := envValue("XDG_RUNTIME_DIR")
		gdm := filepath.Join(runtimeDir, "gdm", "Xauthority")
		if runtimeDir != "" && fileExists(gdm) {
			xauthority = gdm
		} else {
			xauthority = filepath.Join(home, ".Xauthority")
		}
	}
	if strings.ContainsAny(display+xauthority, "\r\n") {
		d.errf("display and Xauthority must not contain newlines")
		return 2
	}
	for _, key := range waylandKeys {
		if strings.ContainsAny(envValue(key), "\r\n") {
			d.errf("desktop session variables must not contain newlines")
			return 2
		}
	}

	plan := fmt.Sprintf(`SSHDESK install plan for account '%s' (Linux):
  - install %s and %d command symlinks in %s
  - write %s (DISPLAY=%s XAUTHORITY=%s RUN_AS=%s)
  - write the sudoers rule %s (desktop server as %s only)
  - add the forced-command snippet %s and reload OpenSSH
  - configure the ydotoold Wayland input helper when the session needs it
OpenSSH itself, system packages, and Tailscale are never installed by this
command; missing capture or input tools are reported at the end.`,
		account, p.binPath(), len(CommandNames)-1, p.binDir,
		p.accountConfig(account), display, xauthority, runAs,
		p.sudoers(account), runAs, p.snippet(account))
	if !d.confirmPlan(plan, opts.Yes) {
		return 0
	}

	self, err := d.SelfPath()
	if err != nil {
		d.errf("could not locate the running binary: %s", err)
		return 1
	}

	steps := []step{
		{"install-binary", func() error {
			if err := os.MkdirAll(p.binDir, 0o755); err != nil {
				return err
			}
			if err := copySelfBinary(self, p.binPath()); err != nil {
				return err
			}
			return installSymlinks(p.binDir, p.binPath())
		}},
		{"write-config", func() error {
			if err := os.MkdirAll(p.configDir, 0o755); err != nil {
				return err
			}
			return writeFileOwned(d, p.accountConfig(account),
				RenderConfig(display, xauthority, runAs, envValue), 0o644, 0, 0)
		}},
		{"write-sudoers", func() error {
			if err := os.MkdirAll(p.sudoersDir, 0o755); err != nil {
				return err
			}
			target := p.sudoers(account)
			if runAs == account {
				if err := os.Remove(target); err == nil {
					d.say(fmt.Sprintf("Removed the stale sudoers rule %s.", target))
				}
				return nil
			}
			if err := writeFileOwned(d, target,
				RenderSudoers(account, runAs, p.binPath()), 0o440, 0, 0); err != nil {
				return err
			}
			visudo, err := d.LookPath("visudo")
			if err != nil {
				if _, statErr := os.Stat("/usr/sbin/visudo"); statErr == nil {
					visudo = "/usr/sbin/visudo"
				} else {
					d.say("note: visudo not found; the sudoers rule was not validated")
					return nil
				}
			}
			if err := d.Run(visudo, "-cf", target); err != nil {
				os.Remove(target)
				return errors.New("visudo rejected the sudoers rule; it was rolled back")
			}
			return nil
		}},
		{"configure-sshd", func() error {
			return configureUnixSshd(d, p.sshdConfig, p.sshdConfigDir,
				p.snippet(account), RenderSshdSnippet(account, p.binDir+"/sshdesk-forced-command"),
				"Include /etc/ssh/sshd_config.d/*.conf", 0o600)
		}},
		{"reload-openssh", func() error { return reloadOpenSSH(d) }},
		{"setup-ydotoold", func() error {
			return setupYdotoold(d, account, uid, gid, p, envValue)
		}},
		{"check-dependencies", func() error {
			warnMissingDependencies(d, envValue)
			return nil
		}},
		{"verify-access", func() error {
			if !sessionDetected {
				d.say(fmt.Sprintf("note: no graphical session detected for user %s; skipping the desktop access check.", account))
				return nil
			}
			args := []string{"-n", "-u", runAs, "env",
				"DISPLAY=" + display, "XAUTHORITY=" + xauthority}
			args = append(args, sessionEnv(envValue)...)
			args = append(args, p.binPath(), "server", "--check")
			if err := d.Run("sudo", args...); err != nil {
				d.say(fmt.Sprintf("warning: the desktop access check as %s failed; verify DISPLAY and XAUTHORITY before connecting", runAs))
			}
			return nil
		}},
	}
	if err := d.runSteps(steps); err != nil {
		d.errf("%s", err)
		return 1
	}

	d.say("")
	d.say("SSHDESK is installed and OpenSSH is running.")
	d.say(fmt.Sprintf("Desktop: ssh -t %s@<server-address> desktop", account))
	d.say(fmt.Sprintf("Plain ssh %s@<server-address> starts a standard shell.", account))
	d.say("Remove it with: sudo sshdesk --uninstall")
	if !sessionDetected {
		d.say("")
		d.say("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
		d.say(fmt.Sprintf("WARNING: no graphical session was detected for user %s.", account))
		d.say("The desktop path will NOT work until that user logs into a")
		d.say("graphical session. After the login, rerun:")
		d.say("  sudo sshdesk --install")
		d.say(fmt.Sprintf("or edit %s manually.", p.accountConfig(account)))
		d.say("!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!")
	}
	return 0
}

// linuxUninstall mirrors the former scripts/uninstall.sh Linux flow. The
// removal order is deliberate: the sshd snippet goes first and OpenSSH is
// validated and reloaded before any file the forced command needs disappears.
func linuxUninstall(d Deps, opts UninstallOptions, p linuxPaths) int {
	if d.Getuid() != 0 {
		d.errf("run the uninstaller as root, for example: sudo sshdesk --uninstall")
		return 1
	}
	account, code := resolveAccount(d, opts.User)
	if code != 0 {
		return code
	}
	if !accountPattern.MatchString(account) {
		d.errf("invalid account name: %s", account)
		return 2
	}

	var plan strings.Builder
	fmt.Fprintf(&plan, "SSHDESK uninstall plan for account '%s' (Linux):\n", account)
	if fileExists(p.snippet(account)) {
		fmt.Fprintf(&plan, "  - remove %s and reload OpenSSH\n", p.snippet(account))
	}
	if fileExists(p.sudoers(account)) {
		fmt.Fprintf(&plan, "  - remove %s\n", p.sudoers(account))
	}
	if opts.KeepConfig {
		fmt.Fprintf(&plan, "  - keep %s (--keep-config)\n", p.accountConfig(account))
	} else if fileExists(p.accountConfig(account)) {
		fmt.Fprintf(&plan, "  - remove %s\n", p.accountConfig(account))
	}
	for _, name := range CommandNames {
		if fileExists(filepath.Join(p.binDir, name)) {
			fmt.Fprintf(&plan, "  - remove %s\n", filepath.Join(p.binDir, name))
		}
	}
	unit := filepath.Join(p.systemdDir, "sshdesk-ydotoold.service")
	if fileExists(unit) {
		plan.WriteString("  - disable and remove sshdesk-ydotoold.service\n")
	}
	if fileExists(filepath.Join(p.modulesLoadDir, "sshdesk-uinput.conf")) {
		fmt.Fprintf(&plan, "  - remove %s\n", filepath.Join(p.modulesLoadDir, "sshdesk-uinput.conf"))
	}
	if fileExists(filepath.Join(p.libexecDir, "ydotoold")) {
		plan.WriteString("  - remove the pinned ydotool helper\n")
	}
	plan.WriteString("OpenSSH itself, the sshd_config Include line (it is a generic setting),\n" +
		"Tailscale, and all other system packages are left untouched.")
	if !d.confirmPlan(plan.String(), opts.Yes) {
		return 0
	}

	steps := []step{
		{"remove-sshd-snippet", func() error {
			target := p.snippet(account)
			if !fileExists(target) {
				d.say(fmt.Sprintf("No sshd snippet for %s; skipping.", account))
				return nil
			}
			if err := os.Remove(target); err != nil {
				return err
			}
			d.say(fmt.Sprintf("Removed %s.", target))
			return nil
		}},
		{"validate-sshd", func() error {
			sshd, err := findSshd(d)
			if err != nil {
				return err
			}
			if err := d.Run(sshd, "-t"); err != nil {
				return errors.New("sshd -t failed after removing the snippet; fix sshd_config and rerun")
			}
			return nil
		}},
		{"reload-openssh", func() error { return reloadOpenSSH(d) }},
		{"remove-sudoers", func() error {
			target := p.sudoers(account)
			if !fileExists(target) {
				d.say(fmt.Sprintf("No sudoers rule for %s; skipping.", account))
				return nil
			}
			if err := os.Remove(target); err != nil {
				return err
			}
			d.say(fmt.Sprintf("Removed %s.", target))
			return nil
		}},
		{"remove-config", func() error {
			target := p.accountConfig(account)
			if opts.KeepConfig {
				d.say(fmt.Sprintf("Keeping %s (--keep-config).", target))
				return nil
			}
			if fileExists(target) {
				if err := os.Remove(target); err != nil {
					return err
				}
				d.say(fmt.Sprintf("Removed %s.", target))
			} else {
				d.say(fmt.Sprintf("No configuration file for %s; skipping.", account))
			}
			if entries, err := os.ReadDir(p.configDir); err == nil && len(entries) == 0 {
				if err := os.Remove(p.configDir); err == nil {
					d.say(fmt.Sprintf("Removed the empty %s directory.", p.configDir))
				}
			}
			return nil
		}},
		{"remove-binaries", func() error {
			removeOwnedCommands(d, p.binDir, p.binPath())
			return nil
		}},
		{"remove-ydotoold", func() error {
			if fileExists(unit) {
				d.Run("systemctl", "disable", "--now", "sshdesk-ydotoold.service")
				if err := os.Remove(unit); err != nil {
					return err
				}
				d.say(fmt.Sprintf("Removed %s.", unit))
				if err := d.Run("systemctl", "daemon-reload"); err != nil {
					return err
				}
				d.say("Reloaded systemd.")
			} else {
				d.say("No sshdesk-ydotoold service; skipping.")
			}
			uinput := filepath.Join(p.modulesLoadDir, "sshdesk-uinput.conf")
			if fileExists(uinput) {
				if err := os.Remove(uinput); err != nil {
					return err
				}
				d.say(fmt.Sprintf("Removed %s.", uinput))
			}
			daemon := filepath.Join(p.libexecDir, "ydotoold")
			if fileExists(daemon) {
				if err := os.Remove(daemon); err != nil {
					return err
				}
				os.Remove(p.libexecDir)
				if fileExists(p.ydotoolCLI) {
					if err := os.Remove(p.ydotoolCLI); err != nil {
						return err
					}
				}
				d.say("Removed the pinned ydotool helper.")
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

// resolveAccount picks the desktop account from --user or the environment.
func resolveAccount(d Deps, requested string) (string, int) {
	account := requested
	if account == "" {
		var err error
		account, err = DetectUser(d.Getenv, d.Logname)
		if err != nil {
			d.errf("%s", err)
			return "", 1
		}
	}
	if !accountPattern.MatchString(account) {
		d.errf("invalid desktop user: %s", account)
		return "", 2
	}
	return account, 0
}

// findSshd locates the sshd binary.
func findSshd(d Deps) (string, error) {
	if path, err := d.LookPath("sshd"); err == nil {
		return path, nil
	}
	if fileExists("/usr/sbin/sshd") {
		return "/usr/sbin/sshd", nil
	}
	return "", errors.New("OpenSSH server is unavailable")
}

// configureUnixSshd installs the forced-command snippet with rollback, shared
// by the Linux and macOS flows. includeLine is prepended to the main config
// when no Include of sshd_config.d exists yet; mainMode is applied when the
// main config is rewritten.
func configureUnixSshd(d Deps, sshdConfig, sshdConfigDir, snippetPath, snippet, includeLine string, mainMode os.FileMode) error {
	if err := os.MkdirAll(sshdConfigDir, 0o755); err != nil {
		return err
	}
	content, err := os.ReadFile(sshdConfig)
	if err != nil {
		return fmt.Errorf("could not read %s: %w", sshdConfig, err)
	}
	addedInclude := false
	if !includePattern.Match(content) {
		backup := sshdConfig + ".before-sshdesk"
		if !fileExists(backup) {
			if err := os.WriteFile(backup, content, mainMode); err != nil {
				return err
			}
		}
		updated := includeLine + "\n" + string(content)
		if err := os.WriteFile(sshdConfig, []byte(updated), mainMode); err != nil {
			return err
		}
		if err := os.Chmod(sshdConfig, mainMode); err != nil {
			return err
		}
		addedInclude = true
	}
	var previous []byte
	hadPrevious := false
	if data, err := os.ReadFile(snippetPath); err == nil {
		previous = data
		hadPrevious = true
	}
	if err := os.WriteFile(snippetPath, []byte(snippet), 0o644); err != nil {
		return err
	}
	rollback := func(failure error) error {
		if hadPrevious {
			os.WriteFile(snippetPath, previous, 0o644)
		} else {
			os.Remove(snippetPath)
		}
		if addedInclude {
			os.WriteFile(sshdConfig, content, mainMode)
		}
		return failure
	}
	sshd, err := findSshd(d)
	if err != nil {
		return rollback(err)
	}
	if err := d.Run(sshd, "-t"); err != nil {
		return rollback(errors.New("OpenSSH rejected the configuration; the SSHDESK snippet was rolled back"))
	}
	return nil
}

// reloadOpenSSH enables and reloads the OpenSSH service, mirroring the former
// start_openssh fallback chain.
func reloadOpenSSH(d Deps) error {
	if _, err := d.LookPath("systemctl"); err == nil {
		if err := d.Run("systemctl", "enable", "--now", "ssh.service"); err == nil {
			return d.Run("systemctl", "reload", "ssh.service")
		}
		if err := d.Run("systemctl", "enable", "--now", "sshd.service"); err == nil {
			return d.Run("systemctl", "reload", "sshd.service")
		}
	}
	if _, err := d.LookPath("service"); err == nil {
		if err := d.Run("service", "ssh", "restart"); err == nil {
			return nil
		}
		if err := d.Run("service", "sshd", "restart"); err == nil {
			return nil
		}
	}
	return errors.New("OpenSSH is configured, but its service could not be started")
}

// setupYdotoold configures the sandboxed ydotoold service for non-GNOME
// Wayland sessions. Missing helper binaries or systemd produce a warning with
// package suggestions instead of a failure; nothing is downloaded. getenv
// resolves session variables (process environment plus harvested values).
func setupYdotoold(d Deps, account string, uid, gid int, p linuxPaths, getenv func(string) string) error {
	family := waylandFamily(getenv)
	if family == "" || family == "gnome" {
		return nil
	}
	daemon := filepath.Join(p.libexecDir, "ydotoold")
	if !fileExists(daemon) {
		if path, err := d.LookPath("ydotoold"); err == nil {
			daemon = path
		}
	}
	cliOK := fileExists(p.ydotoolCLI)
	if !cliOK {
		_, cliErr := d.LookPath("ydotool")
		cliOK = cliErr == nil
	}
	if !cliOK || !fileExists(daemon) {
		d.say("note: the Wayland input helper (ydotool and ydotoold) was not found;")
		d.say("      keyboard and pointer input needs it on this desktop.")
		warnPackageSuggestions(d, "ydotool")
		return nil
	}
	if _, err := d.LookPath("systemctl"); err != nil {
		d.say("warning: automatic Wayland input currently requires systemd; skipping the ydotoold service")
		return nil
	}
	if !fileExists("/dev/uinput") {
		if _, err := d.LookPath("modprobe"); err != nil {
			d.say("warning: /dev/uinput is missing and modprobe is unavailable; skipping the ydotoold service")
			return nil
		}
		if err := d.Run("modprobe", "uinput"); err != nil {
			d.say("warning: could not load the uinput module; skipping the ydotoold service")
			return nil
		}
	}
	if err := os.MkdirAll(p.modulesLoadDir, 0o755); err != nil {
		return err
	}
	uinput := filepath.Join(p.modulesLoadDir, "sshdesk-uinput.conf")
	if err := os.WriteFile(uinput, []byte("uinput\n"), 0o644); err != nil {
		return err
	}
	const socket = "/run/sshdesk-ydotool/socket"
	if err := os.MkdirAll(p.systemdDir, 0o755); err != nil {
		return err
	}
	unit := filepath.Join(p.systemdDir, "sshdesk-ydotoold.service")
	if err := os.WriteFile(unit, []byte(RenderYdotoolUnit(daemon, socket, uid, gid)), 0o644); err != nil {
		return err
	}
	if err := d.Run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := d.Run("systemctl", "enable", "--now", "sshdesk-ydotoold.service"); err != nil {
		return fmt.Errorf("could not start the ydotoold service: %w", err)
	}
	d.say(fmt.Sprintf("Configured the ydotoold Wayland input helper for %s.", account))
	return nil
}

// warnMissingDependencies reports missing capture or streaming tools for the
// current session type. Warnings only; the install still succeeds. getenv
// resolves session variables (process environment plus harvested values).
func warnMissingDependencies(d Deps, getenv func(string) string) {
	family := waylandFamily(getenv)
	missing := func(executable string) bool {
		_, err := d.LookPath(executable)
		return err != nil
	}
	switch family {
	case "":
		if missing("ffmpeg") {
			d.say("note: ffmpeg not found; using the slower MIT-SHM capture fallback")
			warnPackageSuggestions(d, "ffmpeg")
		}
	case "gnome":
		if missing("gst-launch-1.0") || missing("gst-inspect-1.0") ||
			d.Run("gst-inspect-1.0", "pipewiresrc") != nil {
			d.say("note: GNOME PipeWire capture needs GStreamer tools and its PipeWire plugin")
			warnPackageSuggestions(d, "gnome-pipewire")
		}
	case "kde":
		if missing("spectacle") {
			d.say("note: spectacle not found; KDE Wayland capture needs it")
			warnPackageSuggestions(d, "spectacle")
		}
		if missing("ydotool") {
			warnPackageSuggestions(d, "ydotool")
		}
	case "wlroots":
		if missing("grim") {
			d.say("note: grim not found; wlroots Wayland capture needs it")
			warnPackageSuggestions(d, "grim")
		}
		if missing("ydotool") {
			warnPackageSuggestions(d, "ydotool")
		}
	}
}

// warnPackageSuggestions prints one install suggestion per detected package
// manager, mirroring the package names the former install.sh used.
func warnPackageSuggestions(d Deps, what string) {
	packages := func(names ...string) string { return strings.Join(names, " ") }
	type suggestion struct {
		manager string
		command string
	}
	var suggestions []suggestion
	switch what {
	case "ffmpeg":
		suggestions = []suggestion{
			{"apt-get", "apt-get install ffmpeg"},
			{"dnf", "dnf install ffmpeg"},
			{"pacman", "pacman -S ffmpeg"},
		}
	case "gnome-pipewire":
		suggestions = []suggestion{
			{"apt-get", "apt-get install " + packages("gstreamer1.0-tools", "gstreamer1.0-plugins-base", "gstreamer1.0-pipewire")},
			{"dnf", "dnf install " + packages("gstreamer1", "gstreamer1-plugins-base", "pipewire-gstreamer")},
			{"pacman", "pacman -S " + packages("gstreamer", "gst-plugins-base", "gst-plugin-pipewire")},
		}
	case "spectacle":
		suggestions = []suggestion{
			{"apt-get", "apt-get install kde-spectacle"},
			{"dnf", "dnf install spectacle"},
			{"pacman", "pacman -S spectacle"},
		}
	case "grim":
		suggestions = []suggestion{
			{"apt-get", "apt-get install grim"},
			{"dnf", "dnf install grim"},
			{"pacman", "pacman -S grim"},
		}
	case "ydotool":
		suggestions = []suggestion{
			{"apt-get", "apt-get install ydotool"},
			{"dnf", "dnf install ydotool"},
			{"pacman", "pacman -S ydotool"},
		}
	}
	for _, candidate := range suggestions {
		if _, err := d.LookPath(candidate.manager); err == nil {
			d.say("      install it with: sudo " + candidate.command)
			return
		}
	}
	d.say("      on this system install the equivalent of: " + suggestions[0].command)
}
