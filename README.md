# chisel-proxy

developement tool for [debcrafting](https://github.com/canonical/debcraft), [chisel](https://github.com/canonical/chisel) and [chisel-releases](https://github.com/canonical/chisel-releases). it lets you serve your own `.deb`s to chisel without uploading them to an archive first, which lets you write and test slice definitions for a package before it lands upstream.

## why

chisel-proxy stands up an in-memory apt archive holding your debs, signs it with an ephemeral key, and runs an HTTP proxy that answers requests for its own suite from memory while streaming everything else through to the real archive untouched. upstream is never re-signed and always wins on priority. the local archive can only supply names upstream does not have.

## use

```shell
cd ~/git/chisel-releases
chisel-proxy cut ./demo-hello_1.0_amd64.deb ./demo-hello.yaml \
    -- demo-hello_bins base-files_base --root ./rootfs
```

each positional before `--` is one of:

- a `.deb` file, served as-is,
- a directory, packed into a `.deb` on the fly (honouring `DEBIAN/control` if present, else fabricating one),
- a `bin-<name>/` directory or a `.tar.xz` file, treated as a bin package (see below),
- a `.yaml`/`.yml` slice definition, spliced into the release checkout for you.

everything after `--` is forwarded to real `chisel cut`. your checkout is never touched: chisel-proxy copies it to a temp dir, injects the archive + public key + your slices there, and points chisel at the copy. a throwaway cache is used per run so every cut starts clean, and chisel's exit code is passed through. the `chisel` binary is found on `PATH`, or set `CHISEL=/path/to/chisel`.

you can also just run the proxy and wire chisel up yourself:

```shell
chisel-proxy serve ./demo-hello_1.0_amd64.deb
```

it prints the `http_proxy` line, the `chisel.yaml` blocks to paste, and a stub slice.

## bins

chisel's newer bin packages come from the snap store over HTTPS, not from an apt archive. following chisel's own `bin-` naming convention, a `bin-<name>/` directory (or a prebuilt `<name>.tar.xz`) is treated as a bin: chisel-proxy packs it into a `.tar.xz` and serves it by intercepting the snap store's TLS with an ephemeral CA it hands chisel via `SSL_CERT_FILE`. pass its `bin-<name>.yaml` slice alongside:

```shell
CHISEL=/path/to/chisel-with-bins chisel-proxy cut --release ./chisel-releases \
    bin-demo-hello/ bin-demo-hello.yaml -- bin-demo-hello_bins --root ./rootfs
```

bins are Linux only (Go ignores `SSL_CERT_FILE` elsewhere), need a format v3+ checkout (matching chisel), and need a bins-capable chisel on `CHISEL`. each bin payload needs a matching `bin-<name>.yaml` slice among the inputs, and vice versa.

## examples

- `examples/deb/` -- inject a `.deb`, runs anywhere with `chisel` + network.
- `examples/bin/` -- inject a bin over the TLS-intercepted snap store (Linux, bins-capable `CHISEL`).

## build

```shell
make build      # ./bin/chisel-proxy
make test       # unit tests
make e2e        # real chisel cut through the proxy (needs chisel + network)
```
