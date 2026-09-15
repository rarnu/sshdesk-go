# Changelog

## Unreleased

### Breaking

- **Forced-command routing reduced to a single `desktop` selector.** Only the
  exact remote command `desktop` starts the graphical session. A connection
  without a command now opens the authenticated account's login shell, and any
  other original command is passed verbatim to that shell's `-c`, exactly as
  sshd behaves without a `ForceCommand`. The previous defaults and aliases are
  gone: plain `ssh user@server` no longer opens the desktop, and `sshdesk`,
  `sshdesk-server`, `shell`, and `sshdesk-shell` are no longer special routes
  (they now run as ordinary shell commands).
- The dispatcher no longer routes `sshdesk-agent ...` through the
  `sshdesk-agent-ssh` allowlist; agent commands reach the `sshdesk-agent`
  binary in `PATH` through the standard shell channel. The `agent-ssh`
  subcommand and its strict allowlist (exit 126/2) remain installed for
  restricted deployments that point their own forced command at it; its
  rejection message now reads "This account accepts only SSHDESK agent
  commands."
- `sudo -n -u RUN_AS` elevation now applies to the desktop path only; shell
  logins and remote commands always run as the authenticated account. The
  generated sudoers file drops the `sshdesk agent-ssh *` rule and keeps only
  the argument-free `sshdesk server ""` rule.
- `sshdesk-split` now opens its desktop pane with `ssh -t <target> desktop`
  to match the explicit selector.
- The whitelist configuration (`/etc/sshdesk/<user>.conf`, file overrides
  environment, `RUN_AS` validation) is still loaded and exported on every
  path before dispatch, and the desktop PTY requirement and error message are
  unchanged.

### Added

- Linux install now harvests the graphical session environment from the
  target account's active session: `sshdesk --install` scans `/proc` for
  processes owned by the desktop uid (compositor processes such as
  gnome-shell, plasmashell, sway, Hyprland, weston, wayfire, labwc, river,
  and kwin_wayland preferred) and records `WAYLAND_DISPLAY`,
  `XDG_RUNTIME_DIR`, `XDG_SESSION_TYPE`, `XDG_CURRENT_DESKTOP`,
  `DBUS_SESSION_BUS_ADDRESS`, `DISPLAY`, and `XAUTHORITY` from their
  environment. Plain `sudo sshdesk --install` therefore writes a complete
  Wayland configuration without `sudo --preserve-env=...`; the resolution
  order is explicit flag > process environment > /proc harvest > defaults.
  The ydotoold setup and dependency health check use the harvested values
  for the GNOME/KDE/wlroots family decision, and a detected session is
  announced with one output line.

- Built-in cross-platform installer: `sshdesk --install` and
  `sshdesk --uninstall` (also available as the `install`/`uninstall`
  subcommands). The Linux flow (root-only) installs the binary and eight
  symlinks in `/usr/local/bin`, writes `/etc/sshdesk/<user>.conf`, adds a
  visudo-checked sudoers rule when `--run-as` differs from the account,
  installs the sshd forced-command snippet with `sshd -t` validation and
  rollback, reloads OpenSSH, configures the sandboxed `sshdesk-ydotoold`
  service on non-GNOME Wayland sessions, reports missing capture/input tools
  with per-package-manager suggestions, and runs a desktop access check.
  macOS performs a user-level install in `~/.local` (sudo additionally writes
  the sshd snippet and enables Remote Login). Windows (elevated) installs
  `sshdesk.exe` plus `.cmd` wrappers, a PATH registry entry, the sshd_config
  marker block with rollback, the firewall rule, and service startup.
  Uninstall removes only what the installer created, in a safe order — sshd
  snippet first, then `sshd -t` + OpenSSH reload, sudoers rule,
  `/etc/sshdesk` configuration (`--keep-config` preserves it), the binary and
  its symlinks/wrappers (only paths verified to be ours), and the ydotoold
  helper. OpenSSH itself, the `sshd_config` Include line, and system packages
  are never touched. Both commands support `--yes`; install also supports
  `--user`, `--display`, `--xauthority`, and `--run-as`.

### Removed

- The `scripts/` directory and its test-only `internal/installer` package.
  The curl-piped bootstrap, release-binary download with SHA-256 verification,
  automatic package installation (OpenSSH, capture tools, pinned ydotool
  binaries), and the interactive Tailscale offer are gone: the installer never
  downloads code or installs packages anymore. Obtain the `sshdesk` binary
  from a release or `go build`, then run `sshdesk --install`; install
  OpenSSH, capture tools, ydotool, and Tailscale with the system package
  manager when the installer reports them missing.

### Changed

- Linux install is now session-aware end to end. Wayland sessions record the
  harvested variables verbatim (no forced `DISPLAY=:0`); X11 sessions write
  only `DISPLAY` and `XAUTHORITY`; and with no active graphical session at
  all the installer still writes the X11 defaults but prints a prominent
  warning (desktop path unavailable until the user logs into a graphical
  session, then rerun `sudo sshdesk --install` or edit the config) and skips
  the verify-access `--check`, which could only fail. The access check
  environment is guaranteed identical to the generated config.
- Installed configs now default `SSHDESK_SCALE=1.0` (full detail) instead of
  `auto`; the in-program `auto` semantics are unchanged and remain available
  for hand edits.
- The Go module path is now `github.com/rarnu/sshdesk-go`.
- Installer completion hints now point at the explicit desktop selector
  (`ssh -t <user>@<server> desktop`) and note that a plain `ssh` starts a
  standard shell, matching the routing change above.

## 1.0.0 (2026-09-13)

Complete Go rewrite of SSHDESK (previously a Python 3.10+ project). The user
experience, terminal protocols, forced-command routing, agent grammar, and
configuration surface are unchanged; the implementation, packaging, and a few
backend internals are new.

### Changed

- The application is one static `sshdesk` binary. The nine command names
  (`sshdesk`, `sshdesk-server`, `sshdesk-local`, `sshdesk-forced-command`,
  `sshdesk-agent`, `sshdesk-agent-ssh`, `sshdesk-remote`, `sshdesk-split`,
  `sshdesk-bench`) are busybox-style symlinks or subcommands of that binary.
- No Python, venv, or pip on the host. The installers download a SHA-256
  verified release binary (or build from a local checkout) instead of
  installing a Python package.
- Sudoers rules for split SSH/desktop accounts now permit the argument-free
  `/usr/local/bin/sshdesk server ""` and the constrained
  `/usr/local/bin/sshdesk agent-ssh *` instead of the old per-command paths.
- GNOME Wayland capture drives GStreamer through a `gst-launch-1.0 ... ! fdsink`
  subprocess instead of a PyGObject/appsink pipeline; the installer probes
  `gst-launch-1.0`/`gst-inspect-1.0` and installs the GStreamer tools and
  PipeWire plugin rather than PyGObject packages.
- The X11 capture chain is FFmpeg/XCB, then MIT-SHM, then XCB GetImage (the
  Pillow/XCB fallback is replaced by a pure-Go XCB implementation).
- Kitty tile encoding uses a self-contained 128-color octree quantizer in place
  of Pillow FASTOCTREE; tiles are visually and size equivalent but not byte
  identical.
- macOS capture/input use cgo Quartz (`CGDisplayCreateImage`, `CGEvent`);
  Windows capture/input use `BitBlt`/`SendInput` through direct syscalls.
- Scaling uses `x/image/draw` Catmull-Rom where Pillow used LANCZOS/BICUBIC.
- The shell `sshdesk-forced-command` wrapper script is gone; its whitelist
  configuration parsing, PTY checks, and `RUN_AS` sudo elevation now live in
  the binary's forced-command dispatcher with identical semantics.

### Unchanged

- OpenSSH still owns authentication, encryption, host keys, and transport; the
  generated sshd `Match` block, `/etc/sshdesk/USER.conf` whitelist (16 keys,
  file overrides environment), ydotoold systemd sandbox, and pinned ydotool
  1.0.4 checksums are identical.
- Desktop/shell/agent forced-command routing, agent NDJSON session protocol,
  allowlist exit codes (0/1/2/126/130), `sshdesk-remote`/`sshdesk-split`
  clients, terminal capability probing and fallbacks, session scheduling
  (latest-frame pump, idle backoff, backpressure limits, auto render scale),
  statistics overlay, and the benchmark methodology.
