# Platform support

The SSH client side is terminal data, so Linux, macOS, Windows OpenSSH, PuTTY,
mobile SSH apps, and other interactive clients can display SSHDESK. Sharpness is
determined by the terminal emulator, not the operating system.

The one-line bootstrap supports Linux, macOS, and Windows. That means the
application, native backend, and OpenSSH configuration can be installed on each
OS; the host limitations below still apply after installation.

## Linux host

Linux is the primary supported host platform.

### X11

The X11 capture backend detects the desktop size and tries these implementations
in order:

1. a continuously drained FFmpeg/XCB process scaled to the current renderer;
2. MIT-SHM current-frame capture (libX11/libXext shared-memory images);
3. XCB GetImage current-frame capture.

The X11 input backend injects bounded XTest keyboard and mouse events and
releases held state during cleanup. This path is desktop-environment and
distribution neutral as long as the target is an accessible X11 session.

### Wayland

Wayland deliberately prevents generic applications from reading or controlling
other clients. SSHDESK uses tools already designed for the active compositor:

- wlroots: `grim -c` capture;
- GNOME: one persistent Mutter ScreenCast/PipeWire stream, scaled by GStreamer;
- KDE Plasma: `spectacle` capture;
- GNOME input: the linked Mutter RemoteDesktop session;
- other Wayland input: `ydotool` connected to `ydotoold` through `/dev/uinput`.

The graphical user's `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, desktop, and D-Bus
environment must reach the forced command. The installer records these when it
is run from that graphical session. On GNOME it installs the GStreamer
command-line tools, base plugins, and the PipeWire plugin. The Go backend
drives GStreamer through a `gst-launch-1.0 ... ! fdsink` subprocess that emits
raw RGB frames, so no language binding is needed. Mutter provides capture and
input inside the graphical user's existing D-Bus session, so no privileged
helper or screenshot extension is used. The stream is opened once, compositor
frames are drained continuously, and resizing restarts only the local scaling
pipeline.

For KDE and wlroots, the installer creates `sshdesk-ydotoold.service`. That
helper alone opens `/dev/uinput`; its Unix socket is mode 0600 and owned by the
desktop user. SSHDESK itself stays unprivileged. KDE's command-based capture is
functional but slower than GNOME PipeWire, X11, or grim. Compositors that
provide none of the listed capture interfaces need a backend adapter.

## macOS host

The native capture backend uses Quartz `CGDisplayCreateImage` through cgo and
the Quartz input backend uses `CGEvent` injection. Capture stays inside the
`sshdesk` process, so SSH sessions work once TCC grants permission. Input
checks `AXIsProcessTrusted` first and reports the Accessibility requirement
with the same wording as the original implementation. Grant Screen Recording
and Accessibility permission to the installed `sshdesk` binary. A manually
launched server in the logged-in Aqua session is supported; OpenSSH daemon
access still depends on macOS TCC/session policy.

## Windows host

The native capture backend reads the virtual desktop with `GetDC`/`BitBlt` and
the Windows input backend uses `SendInput`, both through direct syscalls with
no cgo. The terminal lifecycle enables and restores Windows virtual-terminal
console modes. The server must execute in the logged-in interactive desktop.
Windows OpenSSH normally runs as a service in Session 0, which can isolate it
from that desktop, so forced-command hosting is currently experimental.
