// Package setup implements the built-in cross-platform installer:
// `sshdesk --install` and `sshdesk --uninstall`. All rendered content
// (per-account config, sshd snippets, sudoers rules, Windows marker blocks,
// the ydotoold unit) is produced by pure functions; side effects flow
// through Deps seams so the full flows are testable on any host.
package setup

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CommandNames lists the nine distributed command names; the first is the
// binary itself, the rest are symlinks (Unix) or .cmd wrappers (Windows).
var CommandNames = []string{
	"sshdesk", "sshdesk-server", "sshdesk-bench", "sshdesk-local",
	"sshdesk-forced-command", "sshdesk-agent", "sshdesk-agent-ssh",
	"sshdesk-split", "sshdesk-remote",
}

// Subcommand maps a command name to the subcommand its wrapper forwards to.
func Subcommand(name string) string {
	switch name {
	case "sshdesk-server":
		return "server"
	case "sshdesk-bench":
		return "bench"
	case "sshdesk-local":
		return "local"
	case "sshdesk-forced-command":
		return "forced-command"
	case "sshdesk-agent":
		return "agent"
	case "sshdesk-agent-ssh":
		return "agent-ssh"
	case "sshdesk-split":
		return "split"
	case "sshdesk-remote":
		return "remote"
	}
	return ""
}

// waylandKeys are inherited from the graphical session when non-empty.
var waylandKeys = []string{
	"WAYLAND_DISPLAY", "XDG_RUNTIME_DIR", "XDG_SESSION_TYPE",
	"XDG_CURRENT_DESKTOP", "DBUS_SESSION_BUS_ADDRESS", "YDOTOOL_SOCKET",
}

// sshdeskDefaults are written with the value auto, except SSHDESK_SCALE,
// which installs as 1.0 (full detail; auto stays available for hand edits).
var sshdeskDefaults = []string{
	"SSHDESK_RENDER", "SSHDESK_COLOR", "SSHDESK_MOUSE", "SSHDESK_UNICODE",
	"SSHDESK_X11_CAPTURE", "SSHDESK_MAX_FPS", "SSHDESK_SCALE",
}

// sshdeskDefaultValue returns the installed default for a SSHDESK_* key.
func sshdeskDefaultValue(key string) string {
	if key == "SSHDESK_SCALE" {
		return "1.0"
	}
	return "auto"
}

// envKeepKeys is the sudoers env_keep whitelist.
var envKeepKeys = []string{
	"DISPLAY", "XAUTHORITY", "WAYLAND_DISPLAY", "XDG_RUNTIME_DIR",
	"XDG_SESSION_TYPE", "XDG_CURRENT_DESKTOP", "DBUS_SESSION_BUS_ADDRESS",
	"YDOTOOL_SOCKET", "SSHDESK_RENDER", "SSHDESK_COLOR", "SSHDESK_MOUSE",
	"SSHDESK_UNICODE", "SSHDESK_X11_CAPTURE", "SSHDESK_MAX_FPS",
	"SSHDESK_SCALE", "TERM",
}

var accountPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// Deps collects every side effect so tests can drive full installs against
// fakes. The real wiring lives in the platform DefaultDeps.
type Deps struct {
	Stdout     io.Writer
	Stderr     io.Writer
	Getenv     func(string) string
	Getuid     func() int
	IsAdmin    func() bool
	LookupUser func(name string) (home string, uid, gid int, err error)
	// CurrentUser returns the name of the account running the process.
	CurrentUser func() (string, error)
	Logname     func() (string, error)
	SelfPath    func() (string, error)
	LookPath    func(string) (string, error)
	Run         func(name string, args ...string) error
	Chown       func(name string, uid, gid int) error
	Confirm     func(plan string) bool
	// HarvestSession, when set, collects graphical-session variables from the
	// target account's running processes (Linux /proc); nil elsewhere.
	HarvestSession func(uid int) map[string]string
	// OnStep, when set, is called with each step name before it runs.
	OnStep func(name string)
}

// InstallOptions are the --install flags.
type InstallOptions struct {
	User       string
	Display    string
	XAuthority string
	RunAs      string
	Yes        bool
}

// UninstallOptions are the --uninstall flags.
type UninstallOptions struct {
	User       string
	Yes        bool
	KeepConfig bool
}

// InstallMain parses --install flags and runs the platform installer.
func InstallMain(argv []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sshdesk --install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	opts := InstallOptions{}
	flags.StringVar(&opts.User, "user", "", "desktop account (default: detected)")
	flags.StringVar(&opts.Display, "display", "", "X11 display (default: $DISPLAY or :0)")
	flags.StringVar(&opts.XAuthority, "xauthority", "", "Xauthority file (default: $XAUTHORITY or ~user/.Xauthority)")
	flags.StringVar(&opts.RunAs, "run-as", "", "desktop owner (default: --user)")
	flags.BoolVar(&opts.Yes, "yes", false, "skip the confirmation prompt")
	if err := flags.Parse(argv); err != nil {
		return 2
	}
	return install(DefaultDeps(stdout, stderr), opts)
}

// UninstallMain parses --uninstall flags and runs the platform uninstaller.
func UninstallMain(argv []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sshdesk --uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	opts := UninstallOptions{}
	flags.StringVar(&opts.User, "user", "", "desktop account (default: detected)")
	flags.BoolVar(&opts.Yes, "yes", false, "skip the confirmation prompt")
	flags.BoolVar(&opts.KeepConfig, "keep-config", false, "keep /etc/sshdesk/<user>.conf")
	if err := flags.Parse(argv); err != nil {
		return 2
	}
	return uninstall(DefaultDeps(stdout, stderr), opts)
}

func (d Deps) say(message string) {
	fmt.Fprintln(d.Stdout, message)
}

func (d Deps) errf(format string, args ...any) {
	fmt.Fprintf(d.Stderr, "sshdesk: "+format+"\n", args...)
}

// step is one named unit of an install or uninstall flow.
type step struct {
	name string
	run  func() error
}

// runSteps executes steps in order, aborting on the first error.
func (d Deps) runSteps(steps []step) error {
	for _, s := range steps {
		if d.OnStep != nil {
			d.OnStep(s.name)
		}
		if err := s.run(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// confirmPlan prints the plan and, unless yes is set, asks for confirmation.
func (d Deps) confirmPlan(plan string, yes bool) bool {
	d.say(plan)
	if yes {
		return true
	}
	if !d.Confirm(plan) {
		d.say("Aborted.")
		return false
	}
	return true
}

// DetectUser mirrors the former installer's SUDO_USER -> USER -> logname
// probing and rejects root as the desktop account.
func DetectUser(getenv func(string) string, logname func() (string, error)) (string, error) {
	user := getenv("SUDO_USER")
	if user == "" || user == "root" {
		user = getenv("USER")
	}
	if user == "" || user == "root" {
		if name, err := logname(); err == nil {
			user = name
		}
	}
	if user == "" || user == "root" {
		return "", errors.New("could not detect the desktop user; rerun with --user USER")
	}
	if !accountPattern.MatchString(user) {
		return "", fmt.Errorf("invalid desktop user: %s", user)
	}
	return user, nil
}

// RenderConfig renders /etc/sshdesk/<account>.conf. Wayland session keys are
// included only when non-empty, so X11 and headless installs stay clean.
func RenderConfig(display, xauthority, runAs string, getenv func(string) string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "DISPLAY=%s\n", display)
	fmt.Fprintf(&b, "XAUTHORITY=%s\n", xauthority)
	fmt.Fprintf(&b, "RUN_AS=%s\n", runAs)
	for _, key := range waylandKeys {
		if value := getenv(key); value != "" {
			fmt.Fprintf(&b, "%s=%s\n", key, value)
		}
	}
	for _, key := range sshdeskDefaults {
		fmt.Fprintf(&b, "%s=%s\n", key, sshdeskDefaultValue(key))
	}
	return b.String()
}

// RenderSudoers renders the per-account sudoers rule. Only the argument-free
// desktop server may run as the desktop owner; root is never granted.
func RenderSudoers(account, runAs, binPath string) string {
	return fmt.Sprintf("Defaults:%s env_keep += \"%s\"\n%s ALL=(%s) NOPASSWD: %s server \"\"\n",
		account, strings.Join(envKeepKeys, " "), account, runAs, binPath)
}

// RenderSshdSnippet renders the Linux/macOS forced-command Match block,
// mirroring the former configure-sshd.sh output.
func RenderSshdSnippet(account, forcedCommand string) string {
	return fmt.Sprintf(`# SSHDESK forced command, managed by sshdesk --install.
Match User %s
    ForceCommand %s
    PermitTTY yes
    DisableForwarding yes
    X11Forwarding no
    AllowTcpForwarding no
    AllowAgentForwarding no
    PermitTunnel no
    GatewayPorts no
    PermitUserRC no
Match all
`, account, forcedCommand)
}

// RenderWindowsBlock renders the marker-delimited sshd_config block used on
// Windows (no GatewayPorts/PermitUserRC lines, matching the former
// install.ps1).
func RenderWindowsBlock(account, forcedCommand string) (begin, end, block string) {
	begin = "# BEGIN SSHDESK " + account
	end = "# END SSHDESK " + account
	block = begin + "\n" + fmt.Sprintf(`Match User %s
    ForceCommand %s
    PermitTTY yes
    DisableForwarding yes
    X11Forwarding no
    AllowTcpForwarding no
    AllowAgentForwarding no
    PermitTunnel no
`, account, forcedCommand) + end
	block = strings.ReplaceAll(block, "\n", "\r\n")
	return begin, end, block
}

// ReplaceMarkedBlock removes any previous marker block for the account and
// appends the new one, mirroring the former install.ps1 edit.
func ReplaceMarkedBlock(content, account, forcedCommand string) string {
	begin, end, block := RenderWindowsBlock(account, forcedCommand)
	pattern := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(begin) + `.*?^` +
		regexp.QuoteMeta(end) + `\r?\n?`)
	base := strings.TrimRight(pattern.ReplaceAllString(content, ""), "\r\n")
	return base + "\r\n\r\n" + block + "\r\n"
}

// RemoveMarkedBlock strips the marker block for the account.
func RemoveMarkedBlock(content, account string) string {
	begin, end, _ := RenderWindowsBlock(account, "")
	pattern := regexp.MustCompile(`(?ms)^` + regexp.QuoteMeta(begin) + `.*?^` +
		regexp.QuoteMeta(end) + `\r?\n?`)
	base := strings.TrimRight(pattern.ReplaceAllString(content, ""), "\r\n")
	return base + "\r\n"
}

// RenderYdotoolUnit renders the sandboxed ydotoold systemd unit, mirroring
// the former install.sh heredoc.
func RenderYdotoolUnit(daemonPath, socket string, uid, gid int) string {
	return fmt.Sprintf(`[Unit]
Description=SSHDESK Wayland input helper
After=systemd-udevd.service

[Service]
Type=simple
ExecStart=%s --socket-path=%s --socket-perm=0600 --socket-own=%d:%d
Restart=on-failure
RestartSec=1
RuntimeDirectory=sshdesk-ydotool
RuntimeDirectoryMode=0755
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
ProtectControlGroups=yes
ProtectKernelLogs=yes
ProtectKernelModules=yes
ProtectKernelTunables=yes
LockPersonality=yes
RestrictSUIDSGID=yes
RestrictAddressFamilies=AF_UNIX
DevicePolicy=closed
DeviceAllow=/dev/uinput rw

[Install]
WantedBy=multi-user.target
`, daemonPath, socket, uid, gid)
}

// RenderWindowsWrapper renders one .cmd wrapper forwarding to a subcommand.
func RenderWindowsWrapper(exePath, subcommand string) string {
	if subcommand == "" {
		return fmt.Sprintf("@\"%s\" %%*\r\n", exePath)
	}
	return fmt.Sprintf("@\"%s\" %s %%*\r\n", exePath, subcommand)
}

// AddPathEntry returns the PATH value with entry appended unless present.
func AddPathEntry(current, entry string) string {
	for _, existing := range strings.Split(current, ";") {
		if strings.EqualFold(existing, entry) {
			return current
		}
	}
	if current == "" {
		return entry
	}
	return current + ";" + entry
}

// RemovePathEntry returns the PATH value without entry.
func RemovePathEntry(current, entry string) string {
	var kept []string
	for _, existing := range strings.Split(current, ";") {
		if existing != "" && !strings.EqualFold(existing, entry) {
			kept = append(kept, existing)
		}
	}
	return strings.Join(kept, ";")
}

// waylandFamily classifies the graphical session: "" for X11/unknown,
// otherwise gnome, kde, or wlroots.
func waylandFamily(getenv func(string) string) string {
	if getenv("XDG_SESSION_TYPE") != "wayland" && getenv("WAYLAND_DISPLAY") == "" {
		return ""
	}
	desktop := strings.ToLower(getenv("XDG_CURRENT_DESKTOP"))
	switch {
	case strings.Contains(desktop, "gnome"), strings.Contains(desktop, "unity"),
		strings.Contains(desktop, "cinnamon"), strings.Contains(desktop, "budgie"):
		return "gnome"
	case strings.Contains(desktop, "kde"), strings.Contains(desktop, "plasma"):
		return "kde"
	default:
		return "wlroots"
	}
}

// sessionEnv returns KEY=value pairs for the inherited Wayland session keys.
func sessionEnv(getenv func(string) string) []string {
	var pairs []string
	for _, key := range waylandKeys {
		if value := getenv(key); value != "" {
			pairs = append(pairs, key+"="+value)
		}
	}
	return pairs
}

// ownsCommandPath reports whether path may be deleted by the uninstaller: a
// symlink resolving to the installed sshdesk binary, or the binary path
// itself. Anything else is left untouched.
func ownsCommandPath(path, binPath string, lstat func(string) (os.FileInfo, error), readlink func(string) (string, error)) bool {
	info, err := lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := readlink(path)
		if err != nil {
			return false
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		return target == binPath
	}
	return path == binPath
}

// copySelfBinary installs the running binary at dest via a temporary file
// and rename, so replacing a running binary never fails with ETXTBSY.
func copySelfBinary(selfPath, dest string) error {
	if selfPath == dest {
		return nil
	}
	data, err := os.ReadFile(selfPath)
	if err != nil {
		return fmt.Errorf("could not read the running binary: %w", err)
	}
	temporary := filepath.Join(filepath.Dir(dest),
		fmt.Sprintf(".sshdesk-install-%d", os.Getpid()))
	if err := os.WriteFile(temporary, data, 0o755); err != nil {
		return fmt.Errorf("could not write %s: %w", temporary, err)
	}
	if err := os.Chmod(temporary, 0o755); err != nil {
		os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, dest); err != nil {
		os.Remove(temporary)
		return fmt.Errorf("could not install %s: %w", dest, err)
	}
	return nil
}

// installSymlinks (re)creates every command name except the binary itself as
// a symlink to binPath. Existing symlinks are replaced; a foreign plain file
// is never overwritten.
func installSymlinks(binDir, binPath string) error {
	for _, name := range CommandNames[1:] {
		link := filepath.Join(binDir, name)
		if info, err := os.Lstat(link); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("%s exists and is not a symlink; refusing to overwrite it", link)
			}
			if err := os.Remove(link); err != nil {
				return err
			}
		}
		if err := os.Symlink(binPath, link); err != nil {
			return fmt.Errorf("could not create %s: %w", link, err)
		}
	}
	return nil
}

// removeOwnedCommands deletes the binary and its symlinks, skipping (with a
// note) anything that is not demonstrably ours.
func removeOwnedCommands(d Deps, binDir, binPath string) {
	for _, name := range CommandNames {
		path := filepath.Join(binDir, name)
		if _, err := os.Lstat(path); err != nil {
			continue
		}
		if !ownsCommandPath(path, binPath, os.Lstat, os.Readlink) {
			d.say(fmt.Sprintf("Keeping %s: not a SSHDESK-installed file.", path))
			continue
		}
		if err := os.Remove(path); err != nil {
			d.errf("could not remove %s: %s", path, err)
			continue
		}
		d.say(fmt.Sprintf("Removed %s.", path))
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeFileOwned writes a file with an exact mode and owner.
func writeFileOwned(d Deps, path string, content string, mode os.FileMode, uid, gid int) error {
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return err
	}
	if err := os.Chmod(path, mode); err != nil {
		return err
	}
	return d.Chown(path, uid, gid)
}

// includePattern matches an Include of the sshd_config.d directory.
var includePattern = regexp.MustCompile(`(?im)^[[:space:]]*Include[[:space:]]+/etc/ssh/sshd_config\.d/\*`)

// validateAccount checks the account name and existence.
func validateAccount(d Deps, account string) (home string, uid, gid int, err error) {
	if !accountPattern.MatchString(account) {
		return "", 0, 0, fmt.Errorf("invalid account name: %s", account)
	}
	home, uid, gid, err = d.LookupUser(account)
	if err != nil {
		return "", 0, 0, fmt.Errorf("user does not exist: %s", account)
	}
	return home, uid, gid, nil
}
