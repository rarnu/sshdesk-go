# SSHDESK

```text
       _____ _____ __  ______  ____________ __ __
      / ___// ___// / / / __ \/ ____/ ___// //_/
      \__ \ \__ \/ /_/ / / / / __/  \__ \/ ,<
     ___/ /___/ / __  / /_/ / /___ ___/ / /| |
    /____//____/_/ /_/_____/_____//____/_/ |_|

        YOUR DESKTOP  //  ONE SSH SESSION  //  ZERO EXTRA PORTS
```

[![Tests](https://github.com/rarnu/sshdesk-go/actions/workflows/test.yml/badge.svg)](https://github.com/rarnu/sshdesk-go/actions/workflows/test.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**[简体中文](README.zh-CN.md)**

> SSHDESK is a full interactive remote desktop delivered entirely through one
> SSH session and displayed directly inside your terminal.

AI coding agents must read [AGENTS.md](AGENTS.md) before modifying this
repository.

## Demo

[![Play the SSHDESK demo on YouTube](https://img.youtube.com/vi/k9qGXJVsxW0/maxresdefault.jpg)](https://www.youtube.com/watch?v=k9qGXJVsxW0 "Play the SSHDESK demo on YouTube")

(The demo records the original Python implementation; the Go rewrite delivers
the same session experience from a single static binary.)

SSHDESK runs as an OpenSSH forced command. OpenSSH authenticates the user,
encrypts the session, and carries the traffic; SSHDESK never implements SSH
and never listens on a port. There is no browser, custom client, VNC/RDP
listener, second password database, or web server. Kitty, Ghostty, and WezTerm
receive sharp real-pixel tiles; every ordinary ANSI terminal receives the
half-block color-cell renderer, so OpenSSH, PuTTY, mobile clients, and
embedded terminals all stay usable.

The whole application is one static Go binary. The nine command names —
`sshdesk`, `sshdesk-server`, `sshdesk-local`, `sshdesk-bench`,
`sshdesk-forced-command`, `sshdesk-agent`, `sshdesk-agent-ssh`,
`sshdesk-remote`, `sshdesk-split` — are busybox-style symlinks or subcommands
of that binary.

## Connecting

Use the SSH client you already have. Only the exact remote command `desktop`
enters the graphical session; everything else is standard SSH:

```bash
# Standard login shell (unchanged OpenSSH behavior)
ssh user@server

# SSHDESK desktop (explicit selector; a PTY is required, hence -t)
ssh -t user@server desktop

# Any other remote command runs verbatim through the login shell's -c
ssh user@server sshdesk-agent info
```

Routing is deliberately boring: a connection without a command opens the
authenticated account's login shell, and any command other than the exact
`desktop` selector is passed to that shell's `-c`, exactly as if no forced
command were installed. Keyboard, mouse, resize events, changed pixels, and
session cleanup all travel through the one SSH PTY. Press `Ctrl+] Ctrl+]` to
leave the desktop.

> [!WARNING]
> Anyone who can authenticate to an SSHDESK account can run the `desktop`
> selector and see and control the active graphical session. Treat it like
> physical console access. Keep a second administrative login available while
> configuring a forced command.

## Installation

There is no curl-piped bootstrap script. Get the `sshdesk` binary from a
[release](https://github.com/rarnu/sshdesk-go/releases) or build it from a
checkout (`go build -o sshdesk ./cmd/sshdesk`, Go 1.27+), then let the binary
install itself with its built-in cross-platform installer. The installer never
downloads anything and never installs system packages; it checks the capture
and input dependencies for your session and prints package suggestions when
something is missing.

### Linux

Run the installer as root (OpenSSH server must already be installed):

```bash
sudo ./sshdesk --install            # add --user alice when detection is wrong
```

It installs the binary and eight command symlinks in `/usr/local/bin`, writes
the per-account `/etc/sshdesk/<user>.conf`, adds the forced-command snippet to
`/etc/ssh/sshd_config.d` (validated with `sshd -t` and rolled back on
failure), reloads OpenSSH, configures the sandboxed `ydotoold` input helper on
non-GNOME Wayland sessions, and verifies desktop access. Linux-only flags:
`--display`, `--xauthority`, and `--run-as`.

On Wayland, preserve the logged-in graphical session's variables so they are
recorded in the configuration:

```bash
sudo --preserve-env=WAYLAND_DISPLAY,XDG_RUNTIME_DIR,XDG_SESSION_TYPE,\
XDG_CURRENT_DESKTOP,DBUS_SESSION_BUS_ADDRESS,YDOTOOL_SOCKET \
  ./sshdesk --install
```

To host the desktop of a different graphical user, use a dedicated SSH account
and let only the desktop path elevate:

```bash
sudo useradd --create-home --shell /bin/bash sshdesk
sudo ./sshdesk --install \
  --user sshdesk --display :0 \
  --xauthority /home/alice/.Xauthority --run-as alice
```

The generated sudoers rule elevates only the argument-free desktop server as
the graphical user and never grants root; shell logins and remote commands
always run as the authenticated account itself.

### macOS

The install is user-level; rerunning with sudo additionally writes the sshd
snippet and enables Remote Login:

```bash
./sshdesk --install
sudo ./sshdesk --install            # optional: sshd snippet + Remote Login
```

macOS still asks for Screen Recording and Accessibility permission for the
installed binary in System Settings > Privacy & Security.

### Windows

Run the installer in an elevated PowerShell:

```powershell
.\sshdesk.exe --install
```

The elevated install copies `sshdesk.exe` under `%ProgramData%\SSHDESK`,
registers the binary directory on the system PATH, adds the forced-command
block to `sshd_config` (validated and rolled back on failure), creates the
OpenSSH firewall rule, and starts the service. Without Administrator rights it
performs a user-level install under `%LOCALAPPDATA%` only. Windows OpenSSH
normally runs in Session 0, so forced-command desktop capture is experimental
and must reach the logged-in interactive desktop.

### Capture and input dependencies

`sshdesk --install` checks these tools and suggests packages for the detected
package manager, but never installs them itself:

| Linux session | Capture | Input |
|---|---|---|
| X11, any desktop | FFmpeg/XCB → MIT-SHM → XCB (`ffmpeg` recommended) | XTest |
| GNOME Wayland | persistent Mutter + PipeWire/GStreamer | Mutter RemoteDesktop API |
| KDE Plasma Wayland | `spectacle` | `ydotool` ≥ 1.0.4 + `ydotoold` |
| wlroots (Sway, Hyprland, …) | `grim` | `ydotool` ≥ 1.0.4 + `ydotoold` |

GNOME needs the GStreamer command-line tools (`gst-launch-1.0`), base plugins,
and the GStreamer PipeWire plugin. Non-GNOME Wayland input requires `ydotoold`
access to `/dev/uinput`; do not run the SSHDESK server itself as root.

### Tailscale (optional)

For access beyond the LAN, install Tailscale yourself (for example
`curl -fsSL https://tailscale.com/install.sh | sh` on Linux). Tailscale simply
carries normal OpenSSH over the private tailnet; it does not replace OpenSSH
or add a second authentication mode.

## Uninstalling

```bash
sudo sshdesk --uninstall            # Linux; add --yes to skip the confirmation
sshdesk --uninstall                 # macOS user-level; sudo removes the sshd snippet too
```

The uninstaller removes only what the installer created — the sshd snippet,
the sudoers rule, the `/etc/sshdesk` configuration, the binary and its
symlinks or wrappers, and the ydotoold helper — validates and reloads OpenSSH
afterwards, and keeps foreign same-named files. OpenSSH itself, the
`sshd_config` Include line, and all system packages are left untouched.
`--keep-config` preserves `/etc/sshdesk/<user>.conf`. See the
[manual installation guide](docs/manual-install.md) for the manual equivalent
of every step.

## Configuration

The installer writes safe defaults to `/etc/sshdesk/<user>.conf`:

```text
DISPLAY=:0
XAUTHORITY=/home/alice/.Xauthority
RUN_AS=alice
SSHDESK_RENDER=auto
SSHDESK_COLOR=auto
SSHDESK_MOUSE=auto
SSHDESK_UNICODE=auto
SSHDESK_X11_CAPTURE=auto
SSHDESK_MAX_FPS=auto
SSHDESK_SCALE=auto
```

Parsing is a fixed 16-key whitelist (`DISPLAY`, `XAUTHORITY`, `RUN_AS`, the
six Wayland session keys, and the seven `SSHDESK_*` knobs) with no shell
evaluation; values in the file override process environment variables.
`RUN_AS` is the desktop-owning account and defaults to the SSH account itself.

## Controls and tuning

- type normally to send keyboard input (Ctrl/Alt/Shift, arrows, F1–F12)
- use the terminal mouse for movement, clicks, drag, and wheel scrolling
- `Ctrl+S` toggles the live statistics overlay
- `Ctrl+] Ctrl+]` always exits locally and is never injected
- resizing the terminal triggers a new viewport and a full redraw without
  disconnecting

Performance targets: 60 FPS sharp / 30 FPS ANSI while active, adaptive idle
presentation, latest-frame scheduling that drops stale work under backpressure
instead of accumulating latency, and dynamic scale-down when the client falls
behind. `SSHDESK_RENDER=kitty` requires Kitty graphics; `ansi` forces the
universal fallback. `SSHDESK_X11_CAPTURE=auto` tries a continuously drained
FFmpeg/XCB stream, then MIT-SHM, then XCB. `SSHDESK_MAX_FPS` accepts 1–120.
`SSHDESK_SCALE` accepts fixed values from 0.25–1.0 (0.75 sends fewer pixels on
slow links) or `auto` for dynamic adjustment.

## Agents and automation

Agent computer-use commands are ordinary remote commands: `ssh user@server
sshdesk-agent info` runs the `sshdesk-agent` command through the standard
shell `-c` channel, and the command set parses a fixed grammar that never
evaluates a received shell string. The stricter `sshdesk-agent-ssh` allowlist
wrapper remains installed for restricted deployments that point their own
forced command at it; the default dispatcher does not route through it. Any
agent that can run CLI commands over SSH can connect — no particular framework
or model is required. Scripted actions at known coordinates need no vision;
navigating an unfamiliar desktop needs vision or a separate OCR/analysis tool,
because observations are screenshots, not an accessibility tree.

```bash
ssh user@server sshdesk-agent info
ssh user@server sshdesk-agent screenshot --max-width 1280 > desktop.png
ssh user@server sshdesk-agent move 900 500
ssh user@server sshdesk-agent click 900 500 --button left
ssh user@server sshdesk-agent scroll -3 900 500
ssh user@server sshdesk-agent type hello
ssh user@server sshdesk-agent key enter
```

Install SSHDESK locally and use `sshdesk-remote` for reliable quoting and
bounded newline-delimited JSON responses:

```bash
sshdesk-remote user@server info
sshdesk-remote user@server screenshot --output desktop.png
sshdesk-remote user@server click 900 500
sshdesk-remote user@server type 'text with spaces'
```

Long-running agents can keep one NDJSON session open:

```bash
sshdesk-remote user@server session
{"id":1,"action":"observe","max_width":1280}
{"id":2,"action":"click","x":900,"y":500,"button":"left"}
{"id":3,"action":"type","text":"hello"}
{"id":4,"action":"quit"}
```

To place a local agent shell beside the remote visual desktop, install `tmux`
and run `sshdesk-split user@server`; the right pane opens the desktop with the
explicit `desktop` selector and the left pane is free for `sshdesk-remote`.
Standard OpenSSH `ControlMaster` configuration can multiplex these sessions
over one connection; SSHDESK never opens another service or port.

## Platform support

Linux is the primary, fully integrated host; any OS works as the SSH client
because the visual protocol is standard terminal output over SSH.

| Host | Capture | Input | Notes |
|---|---|---|---|
| Linux X11 | FFmpeg/XCB → MIT-SHM → XCB fallback chain | XTest | recommended host |
| Linux GNOME Wayland | persistent Mutter + PipeWire stream | Mutter RemoteDesktop | no privileged helper |
| Linux KDE / wlroots | `spectacle` / `grim` screenshots | `ydotool` + `ydotoold` | sandboxed uinput service |
| macOS | Quartz (Retina-aware) | Quartz CGEvent | needs Screen Recording + Accessibility |
| Windows | BitBlt virtual desktop | SendInput | interactive session only; forced command experimental |

See [platform support](docs/platforms.md) for exact backend behavior and
[client compatibility](docs/compatibility.md) for terminal support.

## Security

Anyone who can authenticate sees and controls the live graphical session —
treat an SSHDESK account like physical console access, and keep a second
administrative login while configuring the forced command. The generated
sudoers rule elevates only the argument-free desktop server as the desktop
owner and never grants root; shell logins and remote commands always run as
the authenticated account. The generated `Match` block disables forwarding,
tunnels, and agent forwarding on every path. The Wayland input helper runs as
a sandboxed systemd service restricted to `/dev/uinput`. See
[security and permissions](docs/security.md) for the full model.

## Development and testing

The Go module targets Go 1.27 and builds one static binary:

```bash
go build -o sshdesk ./cmd/sshdesk
go test ./...
go vet ./...
gofmt -l .

# Cross-compile checks
GOOS=linux GOARCH=amd64 go build ./...
GOOS=linux GOARCH=arm64 go build ./...
GOOS=windows GOARCH=amd64 go build ./...

# Benchmark exact rendered terminal bytes
./sshdesk bench --duration 60 --columns 100 --rows 30 --color 256
```

Capture, rendering, input, session management, and terminal output live in
separate packages, so each backend can evolve independently while the OpenSSH
user experience stays unchanged.

## Documentation

- [Architecture and data flow](docs/architecture.md)
- [Platform support](docs/platforms.md)
- [Client and terminal compatibility](docs/compatibility.md)
- [Security and permissions](docs/security.md)
- [Benchmark methodology](docs/benchmark.md)
- [Manual installation guide](docs/manual-install.md)
- [Changelog](CHANGELOG.md)
- [Development plan and status](PLAN.md)

## License

[MIT](LICENSE)

## Acknowledgements

SSHDESK began as the Python project
[rylena/sshdesk](https://github.com/rylena/sshdesk); this repository is the Go
rewrite, and the demo video above records that original implementation. The
sharp renderer builds on the idea demonstrated by
[Desktui](https://github.com/mishushakov/desktui): terminal image pixels and
changed tiles can preserve far more desktop detail than character art.
