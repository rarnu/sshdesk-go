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

AI coding agents must read [AGENTS.md](AGENTS.md) before modifying this
repository.

> SSHDESK is a full interactive remote desktop delivered entirely through an SSH session and displayed directly inside your terminal.

## Demo

[![Play the SSHDESK demo on YouTube](https://img.youtube.com/vi/k9qGXJVsxW0/maxresdefault.jpg)](https://www.youtube.com/watch?v=k9qGXJVsxW0 "Play the SSHDESK demo on YouTube")

(The demo records the original Python implementation; the Go rewrite delivers
the same session experience from a single static binary.)

Connect with the SSH client you already have. Only the exact remote command
`desktop` enters the graphical session; everything else is standard SSH:

```bash
# Standard login shell (unchanged OpenSSH behavior)
ssh desktop@example.com

# SSHDESK desktop (explicit selector)
ssh -t desktop@example.com desktop

# Any other remote command runs through the normal shell
ssh desktop@example.com sshdesk-agent info
```

OpenSSH authenticates the user and launches the SSHDESK dispatcher as a forced
command. The exact remote command `desktop` starts the graphical session
inside that same terminal; every other connection runs the authenticated
account's login shell (or a shell `-c` remote command) exactly as if no forced
command were installed. Keyboard,
mouse, resize events, changed pixels, and session cleanup all travel through the
one SSH PTY. There is no browser, custom SSH client, VNC/RDP listener, second
password database, web server, or additional network port.

Kitty, Ghostty, and WezTerm receive sharp real-pixel tiles. Every ordinary ANSI
terminal receives the lower-resolution color-cell renderer, so OpenSSH, PuTTY,
mobile clients, and embedded SSH terminals remain usable.

> [!WARNING]
> Anyone who can authenticate to an SSHDESK account can run the `desktop`
> selector and see and control the active graphical session. Treat it like
> physical console access. Keep a second administrative login available while
> configuring a forced command.

## Features

- full desktop viewing with changed-tile/cell updates and static-frame suppression
- keyboard, Ctrl/Alt/Shift, arrows, navigation keys, and F1–F12
- mouse movement, left/right/middle click, drag, and wheel scrolling
- dynamic terminal resize with aspect-ratio-preserving viewport recalculation
- persistent top bar and terminal title showing the connected device name
- sharp palette-compressed PNG tiles through Kitty graphics, including tmux passthrough
- true-color, 256-color, 16-color, Unicode, and ASCII fallbacks
- latest-frame scheduling that drops stale work instead of accumulating latency
- 60 FPS sharp / 30 FPS ANSI active targets with adaptive idle presentation
- live FPS, latency, capture, diff, bandwidth, and update instrumentation
- agent-safe screenshot and computer-use commands carried through OpenSSH
- optional tmux side-by-side layout for an agent shell and visual desktop
- terminal restoration and held-input release after disconnects or crashes
- X11, common Wayland desktop, macOS, and Windows backend abstractions
- one static Go binary; command names are symlinks or subcommands, with no
  Python or other runtime dependency on the host

## Installation

SSHDESK installs itself: the single binary carries a built-in cross-platform
installer, so there is nothing to download besides the binary itself. Get
`sshdesk` from a [release](https://github.com/rarnu/sshdesk-go/releases) or
build it from a checkout (`go build -o sshdesk ./cmd/sshdesk`), then run it on
the host.

On Linux, run the installer as root:

```bash
sudo ./sshdesk --install            # add --user alice when detection is wrong
```

It installs the binary and eight command symlinks in `/usr/local/bin`, writes
the per-account `/etc/sshdesk/<user>.conf`, adds the forced-command snippet to
`/etc/ssh/sshd_config.d` (validated with `sshd -t` and rolled back on failure),
reloads OpenSSH, configures the sandboxed `ydotoold` input helper on non-GNOME
Wayland sessions, and verifies desktop access. On Wayland it detects GNOME, KDE
Plasma, or wlroots and reports missing capture or input tools with package
suggestions for the detected package manager; it never installs packages or
downloads anything itself. The OpenSSH server must already be installed.

On macOS, the install is user-level; rerunning with sudo additionally
configures sshd and enables Remote Login:

```bash
./sshdesk --install
sudo ./sshdesk --install            # optional: sshd snippet + Remote Login
```

On Windows, run the installer in an elevated PowerShell:

```powershell
.\sshdesk.exe --install
```

The elevated install registers the binary directory on the system PATH, adds
the forced-command block to `sshd_config` (validated and rolled back on
failure), creates the OpenSSH firewall rule, and starts the service. Without
Administrator rights it performs a user-level install only.

Useful flags: `--user USER` overrides desktop-account detection, `--yes` skips
the confirmation prompt, and Linux also accepts `--display`, `--xauthority`,
and `--run-as` (see [dedicated SSH account](#dedicated-ssh-account)).

> [!IMPORTANT]
> Cross-platform installation does not remove OS security boundaries. macOS
> still asks for Screen Recording and Accessibility access. Windows OpenSSH
> normally runs in Session 0, so Windows forced-command desktop capture remains
> experimental even though the installer itself is supported. Any OS can be the
> SSH client; Linux remains the recommended SSHDESK host.

For network access beyond the LAN, install Tailscale separately
(`curl -fsSL https://tailscale.com/install.sh | sh` on Linux). Tailscale
carries normal OpenSSH over the private tailnet; it does not replace OpenSSH
or add a second SSH authentication mode.

### Repairing a Wayland installation

If an older installation closes with a Wayland capture error or behaves like a
slow screenshot slideshow, log into that computer's graphical desktop, open
its local terminal, and rerun `sudo sshdesk --install`. It upgrades the
configuration, reports the compositor dependencies to install, checks a real
frame, and preserves the existing SSHDESK login. Then retry the ordinary SSH
command from the client.

### Uninstalling

```bash
sudo sshdesk --uninstall            # Linux; add --yes to skip the confirmation
sshdesk --uninstall                 # macOS user-level; sudo removes the sshd snippet too
```

The uninstaller removes only what the installer created (the sshd snippet,
sudoers rule, `/etc/sshdesk` configuration, the binary and its symlinks or
wrappers, and the ydotoold helper), validates and reloads OpenSSH afterwards,
and leaves OpenSSH itself, the `sshd_config` Include line, and all system
packages untouched. `--keep-config` preserves `/etc/sshdesk/<user>.conf`. See
the [manual installation guide](docs/manual-install.md) for details.

## Linux host details

### Manual installation

SSHDESK's installer is distribution-independent. It needs the SSHDESK binary
(a release download or `go build -o sshdesk ./cmd/sshdesk` from a checkout),
an OpenSSH server, and the capture/input tools for the active display stack:

| Linux session | Capture | Input |
|---|---|---|
| X11, any desktop | FFmpeg/XCB, MIT-SHM, or XCB | XTest |
| wlroots (Sway, Hyprland, etc.) | `grim` | `ydotool` + `ydotoold` |
| GNOME Wayland | persistent Mutter + PipeWire/GStreamer | Mutter RemoteDesktop API |
| KDE Plasma Wayland | `spectacle` | `ydotool` + `ydotoold` |

`sshdesk --install` checks for these tools and suggests the packages to
install, but never installs them itself. GNOME needs the GStreamer
command-line tools, base plugins, and the GStreamer PipeWire plugin. Other
Wayland desktops need their listed capture command and ydotool 1.0.4 or newer.
FFmpeg is the preferred X11 capture path. Non-GNOME Wayland input requires
`ydotoold` access to `/dev/uinput`; do not run the whole SSHDESK server as
root.

From the repository on the server (build the binary first):

```bash
go build -o sshdesk ./cmd/sshdesk
sudo ./sshdesk --install
```

On Wayland, preserve the logged-in graphical user's session variables when
running the installer so they are recorded in the configuration:

```bash
sudo --preserve-env=WAYLAND_DISPLAY,XDG_RUNTIME_DIR,XDG_SESSION_TYPE,\
XDG_CURRENT_DESKTOP,DBUS_SESSION_BUS_ADDRESS,YDOTOOL_SOCKET \
  ./sshdesk --install
```

This records the compositor, runtime, D-Bus, and optional ydotool settings. Check the
resulting root-owned `/etc/sshdesk/USER.conf` before enabling the forced command.

Verify backend access first:

```bash
/usr/local/bin/sshdesk-server --check
```

Then connect from another terminal with the explicit desktop selector:

```bash
ssh -t user@server desktop
```

A PTY is required for the desktop; `ssh -T user@server desktop` cannot display
an interactive desktop. Press `Ctrl+] Ctrl+]` to leave.

### Dedicated SSH account

To run the desktop as a different graphical user than the SSH login, use a
dedicated login and let only the desktop entry point execute as the graphical
user:

```bash
sudo useradd --create-home --shell /bin/bash sshdesk
sudo ./sshdesk --install \
  --user sshdesk --display :0 \
  --xauthority /home/alice/.Xauthority --run-as alice
```

The generated sudoers rule only elevates the argument-free desktop server as
the graphical user and does not grant root. Shell logins and remote commands
always run as the authenticated `sshdesk` account itself. OpenSSH remains the
only authentication system.

### Normal SSH shell access

Plain `ssh user@server` opens the account's login shell, and any remote
command other than the exact `desktop` selector is passed verbatim to that
shell's `-c`, exactly as if no forced command were installed. Quoting, pipes,
redirection, and exit codes are handled by the account's own shell, so
existing scripts and tools keep working unchanged.

The shell runs as the authenticated SSH account, never as a different `RUN_AS`
desktop owner. Existing forwarding restrictions (the generated `Match` block
disables forwarding, tunnels, and agent forwarding) remain in effect for every
path.

An SSH client alias can make the desktop connection a single word:

```sshconfig
Host server-desktop
    HostName server
    User user
    RequestTTY force
    RemoteCommand desktop
```

Then run `ssh server-desktop` for SSHDESK and `ssh user@server` for the shell.

## Agent computer use and side-by-side work

Agent computer-use commands are ordinary remote commands: `ssh user@server
sshdesk-agent info` runs the `sshdesk-agent` binary (a symlink installed in
`/usr/local/bin`) through the standard shell `-c` channel, and the
`sshdesk-agent` command set itself parses a fixed grammar that never evaluates
a received shell string. The stricter `sshdesk-agent-ssh` allowlist wrapper
remains installed for restricted deployments that choose to point their own
forced command at it, but the default dispatcher no longer routes through it.
Any AI agent that can run CLI commands and use SSH can connect; SSHDESK
does not require a particular agent framework or model. Normal shell access and
scripted actions at known coordinates do not require vision. To navigate an
unfamiliar graphical desktop dynamically, the agent needs vision or a separate
PNG analysis/OCR tool because observations contain screenshots rather than a
semantic accessibility tree. The remote host must have SSHDESK configured, and
the agent must have valid SSH credentials and network access. Examples:

```bash
ssh user@server sshdesk-agent info
ssh user@server sshdesk-agent screenshot --max-width 1280 > desktop.png
ssh user@server sshdesk-agent move 900 500
ssh user@server sshdesk-agent click 900 500 --button left
ssh user@server sshdesk-agent scroll -3 900 500
ssh user@server sshdesk-agent type hello
ssh user@server sshdesk-agent key enter
```

For reliable quoting and machine-readable responses, install SSHDESK locally
and use `sshdesk-remote`. It sends bounded newline-delimited JSON to the fixed
remote command:

```bash
sshdesk-remote user@server info
sshdesk-remote user@server screenshot --output desktop.png
sshdesk-remote user@server click 900 500
sshdesk-remote user@server type 'text with spaces'
```

Long-running agents can avoid process setup for every action:

```bash
sshdesk-remote user@server session
{"id":1,"action":"observe","max_width":1280}
{"id":2,"action":"click","x":900,"y":500,"button":"left"}
{"id":3,"action":"type","text":"hello"}
{"id":4,"action":"quit"}
```

To place a local agent shell beside the remote visual desktop, install `tmux`
and run:

```bash
sshdesk-split user@server
```

The right pane is the normal SSHDESK connection; the left pane is available to
your agent or shell and can call `sshdesk-remote`. These optional automation
commands are also ordinary authenticated SSH sessions. Standard OpenSSH
`ControlMaster` configuration can multiplex them over an existing connection;
SSHDESK never opens another service or port.

## Controls and tuning

- type normally to send keyboard input
- use the terminal mouse for movement, clicks, drag, and scrolling
- `Ctrl+S` toggles statistics (most terminals cannot distinguish `Ctrl+Shift+S`)
- `Ctrl+] Ctrl+]` always exits locally and is never injected
- terminal resizing triggers a new viewport and full redraw without disconnecting

The installer writes safe defaults to `/etc/sshdesk/USER.conf`:

```text
SSHDESK_RENDER=auto
SSHDESK_COLOR=auto
SSHDESK_MOUSE=auto
SSHDESK_UNICODE=auto
SSHDESK_X11_CAPTURE=auto
SSHDESK_MAX_FPS=auto
SSHDESK_SCALE=auto
```

`SSHDESK_RENDER=kitty` requires sharp graphics; `ansi` forces the universal
fallback. `SSHDESK_X11_CAPTURE=auto` tries a continuously drained FFmpeg/XCB
stream, then MIT-SHM, then XCB. `SSHDESK_MAX_FPS` accepts 1–120.
`SSHDESK_SCALE=auto` dynamically reduces detail when the client terminal falls
behind. Fixed values from 0.25–1.0, such as 0.75, send fewer pixels all the time
for smoother sessions on slower clients or networks.

## macOS and Windows host details

Linux is the primary, fully integrated OpenSSH host. Native Quartz capture and
input on macOS and BitBlt capture plus SendInput on Windows are available for
development and manually launched sessions. Install with the built-in
installer:

```bash
./sshdesk --install                   # macOS, user-level (add sudo for sshd)
```

```powershell
.\sshdesk.exe --install               # Windows, elevated PowerShell
```

macOS requires Screen Recording and Accessibility permission for the installed
`sshdesk` binary. Windows hosting must execute inside the logged-in interactive
desktop; the normal Windows OpenSSH service may be isolated in Session 0, so
forced-command hosting there is experimental. Linux/macOS/Windows terminals are
all supported as clients because the visual protocol remains standard terminal
output over SSH.

See [platform support](docs/platforms.md) for exact backend behavior.

## Development, tests, and benchmark

The Go module targets Go 1.27 and builds one static binary whose command names
are busybox-style symlinks or subcommands:

```bash
go build -o sshdesk ./cmd/sshdesk

./sshdesk server --capture synthetic --no-input
go test ./...
go vet ./...
gofmt -l .
```

Benchmark exact rendered terminal bytes:

```bash
./sshdesk bench --duration 60 --columns 100 --rows 30 --color 256
```

The rewrite keeps capture, rendering, input, session management, and terminal
output in separate packages, so the OpenSSH user experience is unchanged while
each backend can evolve independently.

## Documentation

- [Architecture and data flow](docs/architecture.md)
- [Platform support](docs/platforms.md)
- [Client and terminal compatibility](docs/compatibility.md)
- [Security and permissions](docs/security.md)
- [Benchmark methodology](docs/benchmark.md)
- [Changelog](CHANGELOG.md)

## License

MIT

## Acknowledgements

The sharp renderer builds on the idea demonstrated by
[Desktui](https://github.com/mishushakov/desktui): terminal image pixels and
changed tiles can preserve far more desktop detail than character art.
