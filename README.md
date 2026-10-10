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
- a `.yaml`/`.yml` slice definition, spliced into the release checkout for you.

everything after `--` is forwarded to real `chisel cut` (or `chisel info`, see below). your checkout is never touched: chisel-proxy copies it to a temp dir, injects the archive + public key + your slices there, and points chisel at the copy. a throwaway cache is used per run so every run starts clean, and chisel's exit code is passed through. the `chisel` binary is found on `PATH`, or set `CHISEL=/path/to/chisel`.

`--release` and `--arch` are chisel-proxy's own flags and go before `--`, never after. `--arch` sets the architecture of the served archive and is forwarded to `chisel cut` when given; without it, chisel picks the host architecture, same as the archive does.

`info` takes the same positionals and flags (except `--arch`, which `chisel info` does not have) and forwards to `chisel info` instead. it gets the identical setup, so it sees exactly the release `cut` would see:

```shell
chisel-proxy info ./demo-hello.yaml -- demo-hello_bins
```

you can also just run the proxy and wire chisel up yourself:

```shell
chisel-proxy serve ./demo-hello_1.0_amd64.deb
```

it prints the `http_proxy` line, the `chisel.yaml` blocks to paste, and a stub slice.

see `example/`.

## build

```shell
make build      # ./bin/chisel-proxy
make test       # unit tests
make e2e        # real chisel cut + info through the proxy (needs chisel + network)
```
