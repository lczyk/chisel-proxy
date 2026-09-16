package cli

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/lczyk/chisel-proxy/internal/apt"
	"github.com/lczyk/chisel-proxy/internal/deb"
	"github.com/lczyk/chisel-proxy/internal/proxy"
	"github.com/lczyk/chisel-proxy/internal/release"
	"github.com/lczyk/chisel-proxy/internal/sign"
)

func runServe(args []string) int {
	f, pos, _, err := parseArgs(args, false)
	if err != nil {
		errf(err)
		return 2
	}
	name := f.name
	if name == "" {
		name = defaultName
	}
	arch, err := resolveArch(f.arch)
	if err != nil {
		errf(err)
		return 1
	}

	pkgs, sliceFiles, err := buildInputs(pos, deb.Defaults{Arch: arch, Version: f.version})
	if err != nil {
		errf(err)
		return 1
	}
	if len(sliceFiles) > 0 {
		errf(fmt.Errorf("slice (.yaml) inputs only apply to 'cut'; serve has no release to splice into"))
		return 2
	}

	signer, err := sign.New("chisel-proxy", "chisel-proxy@localhost")
	if err != nil {
		errf(err)
		return 1
	}
	archive, err := apt.New(name, arch, pkgs, signer)
	if err != nil {
		errf(err)
		return 1
	}

	px := proxy.New(archive, log.New(os.Stderr, "[proxy] ", 0))
	addr, stop, err := px.Start(f.port)
	if err != nil {
		errf(err)
		return 1
	}
	defer func() { _ = stop() }()

	if err := printServeInstructions(os.Stdout, addr, archive, signer, f.version); err != nil {
		errf(err)
		return 1
	}

	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	<-sigc

	local, upstream := px.Counts()
	fmt.Fprintf(os.Stderr, "\nserved locally: %d   fetched upstream: %d\n", local, upstream)
	return 0
}

func printServeInstructions(w *os.File, addr string, archive *apt.Archive, signer *sign.Signer, version string) error {
	armor, err := signer.ArmoredPublicKey()
	if err != nil {
		return err
	}
	if version == "" {
		version = "<your-release-version>"
	}
	// A low suggested priority; the stub slice pins the archive anyway, so the
	// exact value only matters if the package name also exists upstream.
	const suggestedPriority = 1
	archives, keys := release.RenderBlocks(archive, signer.KeyID(), armor, version, suggestedPriority)

	fmt.Fprintf(w, "proxy listening on %s\n\n", addr)
	fmt.Fprintf(w, "point chisel at it:\n  export http_proxy=http://%s\n\n", addr)
	fmt.Fprintf(w, "add under 'archives:' in your chisel.yaml (adjust priority to be unique and below your other archives):\n\n%s\n", archives)
	fmt.Fprintf(w, "add under 'public-keys:' in your chisel.yaml:\n\n%s\n", keys)
	for _, p := range archive.Packages() {
		fmt.Fprintf(w, "slice stub for %s (slices/%s.yaml):\n\n%s\n", p.Name, p.Name, release.StubSlice(archive.Suite, p))
	}
	fmt.Fprintf(w, "then, with http_proxy set, run e.g.:\n  chisel cut --release . --root ./rootfs %s_all\n\n", firstPkgName(archive))
	fmt.Fprintln(w, "press Ctrl-C to stop.")
	return nil
}

func firstPkgName(a *apt.Archive) string {
	if pkgs := a.Packages(); len(pkgs) > 0 {
		return pkgs[0].Name
	}
	return "<pkg>"
}
