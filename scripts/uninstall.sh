#!/bin/sh
set -eu

operating_system="$(uname -s)"
requested_user="${SSHDESK_USER-}"
assume_yes=0
keep_config=0

say() {
    printf '%s\n' "$*"
}

fail() {
    say "sshdesk-uninstall: $*" >&2
    exit 1
}

usage() {
    cat <<'EOF'
Usage: uninstall.sh [--user USER] [--yes] [--keep-config]

Removes everything scripts/install.sh and scripts/install-server.sh installed
for one account: the sshd snippet, sudoers rule, /etc/sshdesk configuration,
the sshdesk binary and command symlinks, and the ydotoold helper service.
OpenSSH itself, the sshd_config Include line, Tailscale, and system packages
are never touched.
EOF
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --user)
            [ "$#" -ge 2 ] || fail "--user requires an account name"
            requested_user="$2"
            shift 2
            ;;
        --yes)
            assume_yes=1
            shift
            ;;
        --keep-config)
            keep_config=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            fail "unknown option: $1"
            ;;
    esac
done

case "${operating_system}" in
    Linux|Darwin) ;;
    *) fail "unsupported operating system: ${operating_system}" ;;
esac

if [ "${operating_system}" = "Darwin" ]; then
    [ -n "${requested_user}" ] || requested_user="$(id -un)"
else
    if [ -z "${requested_user}" ]; then
        requested_user="${SUDO_USER-}"
    fi
    if [ -z "${requested_user}" ] || [ "${requested_user}" = "root" ]; then
        requested_user="${USER-}"
    fi
    if [ -z "${requested_user}" ] || [ "${requested_user}" = "root" ]; then
        requested_user="$(logname 2>/dev/null || true)"
    fi
fi
if [ -z "${requested_user}" ] || [ "${requested_user}" = "root" ]; then
    fail "could not detect the desktop user; rerun with --user USER"
fi
case "${requested_user}" in
    -*|*[!A-Za-z0-9_.-]*|'') fail "invalid desktop user: ${requested_user}" ;;
esac
if [ "${operating_system}" = "Linux" ]; then
    getent passwd "${requested_user}" >/dev/null 2>&1 || \
        fail "user does not exist: ${requested_user}"
else
    [ "${requested_user}" = "$(id -un)" ] || \
        fail "run the macOS uninstaller while logged in as ${requested_user}"
fi

# Linux needs root or sudo for every step; on macOS the user-level files do
# not, and the system-wide sshd snippet step is skipped with a note instead.
system_wide=1
if [ "$(id -u)" -eq 0 ]; then
    as_root=""
elif command -v sudo >/dev/null 2>&1; then
    as_root="sudo"
elif [ "${operating_system}" = "Darwin" ]; then
    as_root=""
    system_wide=0
else
    fail "sudo is required"
fi

sshd_snippet="/etc/ssh/sshd_config.d/90-sshdesk-${requested_user}.conf"
sudoers="/etc/sudoers.d/sshdesk-${requested_user}"
account_config="/etc/sshdesk/${requested_user}.conf"
ydotoold_unit="/etc/systemd/system/sshdesk-ydotoold.service"
uinput_conf="/etc/modules-load.d/sshdesk-uinput.conf"
ydotool_daemon="/usr/local/libexec/sshdesk/ydotoold"
ydotool_cli="/usr/local/bin/ydotool"

commands="sshdesk sshdesk-server sshdesk-bench sshdesk-local sshdesk-forced-command sshdesk-agent sshdesk-agent-ssh sshdesk-split sshdesk-remote"
if [ "${operating_system}" = "Darwin" ]; then
    bin_dir="${HOME}/.local/bin"
    install_root="${HOME}/.local/share/sshdesk"
else
    bin_dir="/usr/local/bin"
    install_root=""
fi

find_sshd() {
    if command -v sshd >/dev/null 2>&1; then
        command -v sshd
    elif [ -x /usr/sbin/sshd ]; then
        printf '%s\n' /usr/sbin/sshd
    else
        return 1
    fi
}

# 1. Remove the sshd snippet first so the forced command is gone before the
# daemon is validated and reloaded.
remove_sshd_snippet() {
    if [ "${system_wide}" -eq 0 ]; then
        [ ! -e "${sshd_snippet}" ] || \
            say "note: ${sshd_snippet} needs Administrator (sudo) rights to remove; leaving it"
        return
    fi
    [ -e "${sshd_snippet}" ] || { say "No sshd snippet for ${requested_user}; skipping."; return; }
    ${as_root} rm -f "${sshd_snippet}"
    say "Removed ${sshd_snippet}."
}

# 2. Only after the snippet is gone: prove the remaining configuration is
# valid, then reload OpenSSH so the removal takes effect.
validate_and_reload_sshd() {
    [ "${operating_system}" = "Linux" ] || return 0
    [ "${system_wide}" -eq 1 ] || return 0
    sshd_binary="$(find_sshd)" || fail "OpenSSH server is unavailable"
    ${as_root} "${sshd_binary}" -t || \
        fail "sshd -t failed after removing the snippet; fix sshd_config and rerun"
    if command -v systemctl >/dev/null 2>&1; then
        if ${as_root} systemctl reload ssh.service >/dev/null 2>&1; then
            say "Reloaded OpenSSH (ssh.service)."
            return
        fi
        if ${as_root} systemctl reload sshd.service >/dev/null 2>&1; then
            say "Reloaded OpenSSH (sshd.service)."
            return
        fi
    fi
    if command -v service >/dev/null 2>&1; then
        if ${as_root} service ssh restart >/dev/null 2>&1; then
            say "Restarted OpenSSH (service ssh)."
            return
        fi
        if ${as_root} service sshd restart >/dev/null 2>&1; then
            say "Restarted OpenSSH (service sshd)."
            return
        fi
    fi
    fail "OpenSSH configuration is valid, but the service could not be reloaded"
}

remove_sudoers_file() {
    [ "${system_wide}" -eq 1 ] || return 0
    [ -e "${sudoers}" ] || { say "No sudoers rule for ${requested_user}; skipping."; return; }
    ${as_root} rm -f "${sudoers}"
    say "Removed ${sudoers}."
}

remove_account_config() {
    [ "${keep_config}" -eq 0 ] || { say "Keeping ${account_config} (--keep-config)."; return; }
    [ "${system_wide}" -eq 1 ] || return 0
    removed=0
    if [ -e "${account_config}" ]; then
        ${as_root} rm -f "${account_config}"
        say "Removed ${account_config}."
        removed=1
    else
        say "No configuration file for ${requested_user}; skipping."
    fi
    if [ -d /etc/sshdesk ] && [ -z "$(ls -A /etc/sshdesk 2>/dev/null)" ]; then
        ${as_root} rmdir /etc/sshdesk
        say "Removed the empty /etc/sshdesk directory."
    fi
    [ "${removed}" -eq 1 ] || return 0
}

# 5. Remove the binary and symlinks, but only paths that are demonstrably
# ours: symlinks must resolve to the sshdesk binary, and the only plain file
# removed is the binary itself.
remove_one_command() {
    path="$1"
    [ -e "${path}" ] || [ -L "${path}" ] || return 0
    if [ -L "${path}" ]; then
        target="$(readlink "${path}")"
        case "${target}" in
            sshdesk|"${bin_dir}/sshdesk") ;;
            *)
                if [ -z "${install_root}" ] || [ "${target}" != "${install_root}/sshdesk" ]; then
                    say "Keeping ${path}: symlink points to ${target}, not sshdesk."
                    return 0
                fi
                ;;
        esac
    elif [ "${path}" != "${bin_dir}/sshdesk" ]; then
        say "Keeping ${path}: not a symlink into sshdesk."
        return 0
    fi
    if [ "${system_wide}" -eq 0 ]; then
        rm -f "${path}"
    else
        ${as_root} rm -f "${path}"
    fi
    say "Removed ${path}."
}

remove_binaries() {
    for command in ${commands}; do
        remove_one_command "${bin_dir}/${command}"
    done
    if [ -n "${install_root}" ] && [ -d "${install_root}" ]; then
        rm -rf "${install_root}"
        say "Removed ${install_root}."
    fi
}

# 6. The Wayland input helper, only when its unit exists (Linux only). The
# pinned binaries count as ours when the libexec path exists.
remove_ydotoold() {
    [ "${operating_system}" = "Linux" ] || return 0
    [ "${system_wide}" -eq 1 ] || return 0
    if [ -e "${ydotoold_unit}" ]; then
        ${as_root} systemctl disable --now sshdesk-ydotoold.service >/dev/null 2>&1 || true
        ${as_root} rm -f "${ydotoold_unit}"
        say "Removed ${ydotoold_unit}."
        ${as_root} systemctl daemon-reload
        say "Reloaded systemd."
    else
        say "No sshdesk-ydotoold service; skipping."
    fi
    if [ -e "${uinput_conf}" ]; then
        ${as_root} rm -f "${uinput_conf}"
        say "Removed ${uinput_conf}."
    fi
    if [ -e "${ydotool_daemon}" ]; then
        ${as_root} rm -f "${ydotool_daemon}"
        ${as_root} rmdir "$(dirname "${ydotool_daemon}")" 2>/dev/null || true
        [ ! -e "${ydotool_cli}" ] || ${as_root} rm -f "${ydotool_cli}"
        say "Removed the pinned ydotool helper."
    fi
}

print_plan() {
    say "SSHDESK uninstall plan for account '${requested_user}' (${operating_system}):"
    [ ! -e "${sshd_snippet}" ] || say "  - remove ${sshd_snippet} and reload OpenSSH (Linux)"
    [ ! -e "${sudoers}" ] || say "  - remove ${sudoers}"
    if [ "${keep_config}" -eq 1 ]; then
        say "  - keep ${account_config} (--keep-config)"
    else
        [ ! -e "${account_config}" ] || say "  - remove ${account_config}"
    fi
    for command in ${commands}; do
        [ ! -e "${bin_dir}/${command}" ] && [ ! -L "${bin_dir}/${command}" ] || \
            say "  - remove ${bin_dir}/${command}"
    done
    [ -z "${install_root}" ] || [ ! -d "${install_root}" ] || say "  - remove ${install_root}/"
    [ ! -e "${ydotoold_unit}" ] || say "  - disable and remove sshdesk-ydotoold.service"
    [ ! -e "${uinput_conf}" ] || say "  - remove ${uinput_conf}"
    [ ! -e "${ydotool_daemon}" ] || say "  - remove the pinned ydotool helper"
    say "OpenSSH itself, the sshd_config Include line (it is a generic setting),"
    say "Tailscale, and all other system packages are left untouched."
}

if [ "${assume_yes}" -eq 0 ]; then
    print_plan
    if ! ( : >/dev/tty ) 2>/dev/null; then
        fail "no interactive terminal; rerun with --yes to confirm the plan above"
    fi
    printf 'Proceed with the uninstall? [y/N] ' >/dev/tty
    IFS= read -r answer </dev/tty || answer=""
    case "${answer}" in
        y|Y|yes|YES|Yes) ;;
        *) say "Aborted."; exit 0 ;;
    esac
fi

remove_sshd_snippet
validate_and_reload_sshd
remove_sudoers_file
remove_account_config
remove_binaries
remove_ydotoold

say "Uninstall complete."
