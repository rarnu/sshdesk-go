#!/bin/sh
set -eu

script_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
install_root="${HOME}/.local/share/sshdesk"
bin_dir="${HOME}/.local/bin"

# Single Go binary; the command names are symlinks onto it. The binary comes
# from SSHDESK_BINARY or a build placed next to this script (scripts/install.sh
# downloads one).
binary="${SSHDESK_BINARY-}"
if [ -z "${binary}" ]; then
    if [ -x "${script_dir}/sshdesk" ]; then
        binary="${script_dir}/sshdesk"
    elif [ -x "${script_dir}/../sshdesk" ]; then
        binary="${script_dir}/../sshdesk"
    else
        echo "no SSHDESK binary found; set SSHDESK_BINARY or run scripts/install.sh" >&2
        exit 1
    fi
fi
[ -x "${binary}" ] || { echo "SSHDESK binary is not executable: ${binary}" >&2; exit 1; }

mkdir -p "${install_root}" "${bin_dir}"
install -m 0755 "${binary}" "${install_root}/sshdesk"

for command in \
    sshdesk \
    sshdesk-server \
    sshdesk-agent \
    sshdesk-agent-ssh \
    sshdesk-remote \
    sshdesk-split \
    sshdesk-local \
    sshdesk-bench \
    sshdesk-forced-command
do
    ln -sfn "${install_root}/sshdesk" "${bin_dir}/${command}"
done

echo "Installed SSHDESK in ${install_root}."
echo "Add ${bin_dir} to PATH, then grant Screen Recording and Accessibility"
echo "permission to ${install_root}/sshdesk in System Settings > Privacy & Security."
