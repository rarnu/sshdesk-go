# Security and permissions

SSHDESK delegates authentication, encryption, host verification, PTY setup, and
connection management to OpenSSH. It adds no credentials and opens no listening
socket.

## Reporting security issues

If you notice a security issue, do not open a public issue. Email
[rylen.anil@gmaail.com](mailto:rylen.anil@gmaail.com) with the details.

## Process privileges

Run `sshdesk-server` as the graphical desktop user, never as root. X11 capture
and XTest input require access to the target `DISPLAY` and its Xauthority cookie.
Wayland capture needs the logged-in session's runtime/D-Bus environment. GNOME
capture and input use linked Mutter sessions as the unprivileged graphical user;
there is no screenshot extension, root process, or input device helper on this
path. On other Wayland desktops, ydotool input needs a narrowly configured
`ydotoold` with `/dev/uinput` access. On systemd Linux hosts, `sshdesk --install` isolates that helper in
`sshdesk-ydotoold.service`, exposes a mode-0600 Unix socket owned by the desktop
user, and restricts the service device policy to `/dev/uinput`. The helper
binaries themselves come from the distribution or the official ydotool release;
the installer never downloads or builds them, it only reports the packages to
install when they are missing. The installer also loads the `uinput`
kernel module at boot when it is modular. GNOME's ScreenCast and RemoteDesktop
interfaces are compositor-private APIs, so compatibility is validated by the
installer's live capture/input preflight rather than assumed from a version
number.
Keep session credentials private. If a different dedicated SSH account is used,
granting it access to the graphical session is effectively granting console control.
The MIT-SHM capture segment is created mode 0600 and immediately marked for
automatic removal; it remains attached only for the lifetime of the session.
The preferred FFmpeg backend is launched with a fixed argument vector containing
only validated dimensions, frame rate, and the configured X11 display. Client
data is never passed to FFmpeg or a shell.

The installed artifact is one static `sshdesk` binary; the command names used
by sshd and sudoers are symlinks (or subcommands) of that binary, so there is
no interpreter or package directory to keep consistent. The installer is built
into that same binary (`sshdesk --install`), so installation adds no downloaded
code beyond the binary the operator already obtained.

The installer places non-secret terminal/display settings in a root-owned,
read-only-to-users mode-0644 file at
`/etc/sshdesk/USER.conf`. The forced-command dispatcher parses only fixed keys
and does not evaluate the file as code.

When the SSH login and desktop owner differ, the installer creates a per-login
sudoers file. It preserves only fixed display/render environment keys and
permits only the argument-free `sshdesk server` as the desktop owner — the
single path the dispatcher may elevate. It never grants a root command, and
shell or remote-command sessions always run as the authenticated SSH login,
never through this sudo rule or the configured `RUN_AS` desktop owner.

## OpenSSH boundary

The included configuration installs the SSHDESK dispatcher as the account's
forced command and disables forwarding, agent forwarding, tunnels, and user rc
files for the matched account. The dispatcher deliberately changes as little
of the standard SSH attack surface as possible: only the exact remote command
`desktop` starts the graphical session (which also requires a PTY), a
connection without a command opens the account's login shell, and every other
original command is passed verbatim to that shell's `-c` — byte for byte what
sshd does without a `ForceCommand`. Anyone who can authenticate to the account
therefore has exactly the access an ordinary SSH login grants, plus the
ability to view and control the active graphical desktop through the
`desktop` selector.

The `sshdesk-agent` command set is reachable through the standard remote
command channel and parses a fixed grammar that never evaluates a received
shell string; the stricter `sshdesk-agent-ssh` allowlist wrapper (basename
check, `--output` ban, exit 126) remains installed for restricted deployments
that point their own forced command at it, but the default dispatcher does not
route through it. Keep the global OpenSSH `PermitUserEnvironment no` default;
that directive is not portable inside a `Match` block.

Configure public-key, password, multifactor, source-address, and rate-limit
policy in OpenSSH as usual. Test authentication before applying `ForceCommand`,
and always run `sshd -t` before reloading sshd.

## Runtime input

Terminal and agent input is untrusted even after SSH authentication. The parser caps a
pending escape sequence at 8192 bytes. Terminal dimensions, key actions, mouse
buttons, scroll amounts, and translated desktop coordinates are checked or
bounded before injection. Agent lines, screenshots, text, coordinates, wait
times, button counts, and response sizes are bounded. Terminal bytes and agent
fields are never interpreted as shell commands by SSHDESK. Input entered after
selecting the normal login shell is interpreted by that shell as expected.

Captured pixels are converted either to numeric ANSI colors or base64-encoded,
palette-compressed PNG image payloads. Desktop bytes and text are never copied into
terminal commands, preventing captured content from becoming terminal control
sequences. Capability-reported dimensions are bounded before allocation.

On disconnect or failure, SSHDESK releases held synthetic buttons and keys,
closes capture connections/processes, disables mouse reporting, leaves the
alternate screen, and restores POSIX PTY or Windows console attributes.

Anyone who can authenticate to this account can view and control the active
desktop. Treat SSHDESK access as equivalent to physical console access.
