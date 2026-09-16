#!/bin/sh -e
#
# Inject a not-yet-published "bin" package into a chiselled rootfs using
# chisel-proxy.
#
# The bin payload (bin-demo-hello/) and its slice (bin-demo-hello.yaml) live
# beside this script; the release is a minimal format-v3 checkout under
# chisel-releases/. A bin-*/ directory is classified as a bin package (chisel's
# own naming convention): chisel-proxy packs it into a tar.xz, splices the slice,
# and serves it over a TLS-intercepted snap store, while everything else comes
# from the real archive.
#
# The interception relies on SSL_CERT_FILE: honoured on Linux, and on macOS by
# a chisel built with Go 1.27+. Needs a bins-capable chisel (built from a branch
# with bin support); point CHISEL at it:
#   CHISEL=/path/to/chisel-with-bins ./examples/bin/example.sh

PKG="demo-hello"
HERE="$(cd "$(dirname "$0")" && pwd)"
REPO="$(cd "$HERE/../.." && pwd)"
WORK="$HERE/work"

case "$(uname -s)" in
    (Linux|Darwin) ;;
    (*) printf 'example: bin injection needs Linux, or macOS with a chisel built with Go 1.27+\n' >&2; exit 1 ;;
esac
if [ -z "$CHISEL" ] || ! command -v "$CHISEL" >/dev/null 2>&1; then
    printf 'example: set CHISEL to a bins-capable chisel (built from a branch with bin support)\n' >&2
    exit 1
fi

mkdir -p "$WORK"

# build chisel-proxy
go -C "$REPO" build -o "$WORK/chisel-proxy" ./cmd/chisel-proxy

# cut rootfs: bin-demo-hello/ (a bin-*/ dir) is packed into a tar.xz and
# bin-demo-hello.yaml is spliced in; chisel-proxy MITMs the snap store.
rm -rf "$WORK/rootfs"
mkdir -p "$WORK/rootfs"
CHISEL="$CHISEL" "$WORK/chisel-proxy" cut \
    --release "$HERE/chisel-releases" \
    "$HERE/bin-$PKG" \
    "$HERE/bin-$PKG.yaml" \
    -- \
    "bin-${PKG}_bins" --root "$WORK/rootfs"
