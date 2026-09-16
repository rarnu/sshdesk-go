#!/usr/bin/env bash
# build.sh — cross-compile sshdesk for every supported platform/arch.
#
# Usage:
#   scripts/build.sh                     # all six targets into dist/
#   scripts/build.sh OUTDIR              # all six targets into OUTDIR
#   scripts/build.sh OUTDIR TARGETS...   # only the named targets
#
# Targets: windows-amd64 windows-arm64 linux-amd64 linux-arm64
#          darwin-amd64 darwin-arm64
#
# darwin targets need a macOS host: the native capture/input backends use
# cgo (Quartz). linux/windows targets build anywhere with CGO_ENABLED=0.
set -euo pipefail

cd "$(dirname "$0")/.."

all_targets=(windows-amd64 windows-arm64 linux-amd64 linux-arm64 darwin-amd64 darwin-arm64)

is_target() {
    local t
    for t in "${all_targets[@]}"; do
        [[ "$1" == "$t" ]] && return 0
    done
    return 1
}

outdir="dist"
if [[ $# -gt 0 ]] && ! is_target "$1"; then
    outdir="$1"
    shift
fi

if [[ $# -gt 0 ]]; then
    targets=("$@")
    for t in "${targets[@]}"; do
        is_target "$t" || { echo "error: unknown target '$t'" >&2; exit 2; }
    done
else
    targets=("${all_targets[@]}")
fi

host_is_darwin=false
[[ "$(uname -s)" == "Darwin" ]] && host_is_darwin=true

mkdir -p "$outdir"

for target in "${targets[@]}"; do
    goos="${target%-*}"
    goarch="${target#*-}"
    cgo=0
    if [[ "$goos" == "darwin" ]]; then
        if ! $host_is_darwin; then
            echo "error: darwin targets require a macOS host (cgo Quartz backends)" >&2
            exit 1
        fi
        cgo=1
    fi
    out="$outdir/sshdesk-$goos-$goarch"
    [[ "$goos" == "windows" ]] && out="$out.exe"
    echo "==> $goos/$goarch (cgo=$cgo) -> $out"
    CGO_ENABLED=$cgo GOOS=$goos GOARCH=$goarch \
        go build -trimpath -ldflags="-s -w" -o "$out" ./cmd/sshdesk
done

echo "done:"
ls -lh "$outdir"
