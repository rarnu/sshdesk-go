# Changelog

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
