# Architecture

SSHDESK is a server-side terminal application launched by OpenSSH as a forced
command. The client is any normal SSH client with an interactive terminal. The
Go build ships one `sshdesk` binary; the nine command names are symlinks onto
it (busybox-style `argv[0]` dispatch) or subcommands of `sshdesk` itself.

```text
normal SSH client and terminal
          │ terminal graphics or ANSI / PTY input
          │ one authenticated SSH session
          ▼
OpenSSH sshd ── ForceCommand ── sshdesk-forced-command (internal/forcedcmd)
                                  ├── internal/capture
                                  │   ├── x11
                                  │   │   ├── ffmpeg (FFmpeg/XCB stream)
                                  │   │   ├── xshm (MIT-SHM fallback)
                                  │   │   └── XCB GetImage fallback
                                  │   ├── gnome (Mutter/PipeWire + gst-launch)
                                  │   ├── wayland (grim/spectacle/gnome-screenshot)
                                  │   ├── native (macOS Quartz / Windows BitBlt)
                                  │   └── synthetic
                                  ├── internal/render
                                  │   ├── kitty (sharp PNG tiles)
                                  │   ├── ansi (color-cell fallback)
                                  │   └── probe (terminal graphics probe)
                                  ├── internal/input
                                  │   ├── x11 (XTest)
                                  │   ├── mutter (GNOME RemoteDesktop)
                                  │   ├── ydotool
                                  │   ├── quartz (macOS)
                                  │   ├── sendinput (Windows)
                                  │   └── terminal (escape-sequence parser)
                                  └── internal/session (DirectSession + stats)
```

OpenSSH owns authentication, encryption, host keys, PTY allocation, window-size
messages, connection management, and network transport. SSHDESK does not
implement SSH, listen on a port, or define client credentials.

`capture`, `render`, and `input` are independent packages behind small
interfaces. Linux selects X11 or the active Wayland compositor automatically;
native macOS and Windows implementations use the same interfaces. GNOME links
one ScreenCast stream to one RemoteDesktop input session, while other Wayland
backends remain isolated behind the same capture/input abstractions.

The session probes terminal graphics and pixel geometry before starting its input
goroutine. A capable terminal receives palette-compressed PNG tiles through the
Kitty graphics protocol; other terminals receive colored half-block cells. The
session keeps rendered state in both modes. A changed frame becomes either a
full redraw or a tile/cell delta; an unchanged frame produces no frame output.
The preferred X11 backend continuously drains an FFmpeg/XCB stream already
scaled to the renderer's exact target. An independent capture worker keeps only
one latest frame, so rendering or SSH backpressure drops intermediate work
instead of queuing stale frames. MIT-SHM and XCB GetImage remain automatic
fallbacks. Every backend preserves full-desktop coordinates for input. A small
capture-level fingerprint (blake2s digest) lets identical frames reuse the
previous rendered state without rebuilding terminal cells or tiles.
GNOME follows the same persistent-stream model: Mutter publishes a PipeWire
node once per session, a `gst-launch-1.0` pipeline continuously drains and
scales it into raw RGB frames, and input is sent through the linked Mutter
RemoteDesktop object over D-Bus. Terminal resize only rebuilds the local
scaling pipeline, not the compositor session.
Input parsing and desktop injection run in a dedicated goroutine so slow
terminal output does not starve keyboard or mouse events. The capture rate
targets 60 FPS in sharp mode or 30 FPS in ANSI mode and stays fresh at that
rate. Presentation backs off to 30 FPS during light activity and 2 FPS while
idle, but input wakes it immediately without restarting capture. The active
limit can be configured from 1 through 120 FPS. In automatic scale mode,
terminal write time and reply latency can lower the render scale to reduce the
target capture size until the client catches up. A fixed render scale from 0.25
through 1.0 can force the same fewer-pixels-per-frame tradeoff.

There is deliberately no SSHDESK application transport or client binary. An
unmodified SSH client carries one PTY byte stream. Kitty graphics, ANSI/UTF-8,
keyboard reports, and mouse reports are terminal protocols inside that stream.

## Agent path

Agent computer use is an optional, low-frequency control path. OpenSSH invokes
the `sshdesk-agent` command without a PTY as an ordinary remote command: the
forced-command dispatcher passes it verbatim to the account's shell `-c`,
which executes the `sshdesk-agent` symlink in `/usr/local/bin`. Requests are
bounded newline-delimited JSON because they are infrequent control operations;
PNG observations are base64 encoded in responses. The desktop's interactive
frame path remains the direct terminal stream and never uses JSON.

```text
agent ── sshdesk-remote ── OpenSSH ── shell -c "sshdesk-agent session"
                                      ├── internal/capture
                                      └── internal/input
```

The `sshdesk-agent` command set parses a fixed grammar and never evaluates a
received shell string. The stricter `sshdesk-agent-ssh` allowlist wrapper
(fixed basename check, `--output` ban, exit 126) remains installed for
restricted deployments that point their own forced command at it; the default
dispatcher no longer routes through it. Standard OpenSSH connection
multiplexing can reuse a transport for repeated agent calls.

## Standard shell path

Only the exact remote command `desktop` selects the graphical session (PTY
required; `RUN_AS` sudo elevation applies here alone). With no remote command
the dispatcher execs the authenticated account's interactive login shell; any
other original command is passed verbatim to that shell's `-c`, exactly as
sshd runs it without a ForceCommand. The shell always runs as the
authenticated SSH account and never uses the `RUN_AS` elevation.

```text
OpenSSH ForceCommand dispatcher
    |-- desktop ---------------- sshdesk-server (PTY required, RUN_AS sudo)
    |-- no command ------------- authenticated account login shell
    `-- anything else ---------- login shell -c <original command>
```
