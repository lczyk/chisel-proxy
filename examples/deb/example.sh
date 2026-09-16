#!/bin/sh -e
#
# Inject a local .deb into a chiselled rootfs using chisel-proxy.
#
# The package payload (demo-hello/) and its slice definition (demo-hello.yaml)
# live beside this script as real files. The script builds chisel-proxy, clones
# a chisel-releases checkout, and cuts a rootfs, passing both the package dir and
# the slice file as inputs (chisel-proxy packs the deb and splices the slice):
# the proxy serves demo-hello from a local signed archive while everything else
# is fetched from the real Ubuntu archive. All generated files land in work/.
#
# Needs the `chisel` binary on PATH (or CHISEL=/path/to/chisel) and network.

RELEASE="${RELEASE:-ubuntu-24.04}"
PKG="demo-hello"
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
WORK="$HERE/work"
CHISEL="${CHISEL:-chisel}"

if ! command -v "$CHISEL" >/dev/null 2>&1; then
    printf 'example: need the chisel binary on PATH, or set CHISEL=/path/to/chisel\n' >&2
    exit 1
fi

mkdir -p "$WORK"

# build chisel-proxy
go -C "$REPO" build -o "$WORK/chisel-proxy" ./cmd/chisel-proxy

# clone chisel-releases
if [ ! -d "$WORK/chisel-releases" ]; then
    git clone --quiet --depth 1 -b "$RELEASE" https://github.com/canonical/chisel-releases "$WORK/chisel-releases"
fi

# cut rootfs: the demo-hello/ dir is packed into a .deb and demo-hello.yaml is
# spliced into the release checkout, both by chisel-proxy.
rm -rf "$WORK/rootfs"
mkdir -p "$WORK/rootfs"
CHISEL="$CHISEL" "$WORK/chisel-proxy" cut \
    --release "$WORK/chisel-releases" \
    "$HERE/$PKG" \
    "$HERE/$PKG.yaml" \
    -- \
    "${PKG}_bins" "${PKG}_docs" base-files_base --root "$WORK/rootfs"
