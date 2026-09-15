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
