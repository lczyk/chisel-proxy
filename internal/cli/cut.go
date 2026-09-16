package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/lczyk/chisel-proxy/internal/apt"
	"github.com/lczyk/chisel-proxy/internal/deb"
	"github.com/lczyk/chisel-proxy/internal/proxy"
	"github.com/lczyk/chisel-proxy/internal/release"
	"github.com/lczyk/chisel-proxy/internal/sign"
)

func runCut(args []string) int {
	f, pos, fwd, err := parseArgs(args, true)
	if err != nil {
		errf(err)
		return 2
	}
	if len(fwd) == 0 {
		errf(fmt.Errorf("cut needs chisel arguments after '--', e.g.:\n" +
			"  chisel-proxy cut ./foo.deb -- foo_bins --root ./rootfs"))
		return 2
	}
	for _, a := range fwd {
		if a == "--release" || strings.HasPrefix(a, "--release=") {
			errf(fmt.Errorf("--release is chisel-proxy's own flag; put it before '--', not after (it points chisel at the injected temp copy)"))
			return 2
		}
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
	releaseDir := f.release
	if releaseDir == "" {
		releaseDir = "."
	}

	pkgs, sliceFiles, err := buildInputs(pos, deb.Defaults{Arch: arch, Version: f.version})
	if err != nil {
		errf(err)
		return 1
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

	tmpRelease, cleanupRel, err := release.Inject(releaseDir, archive, signer, f.version, sliceFiles)
	if err != nil {
		errf(err)
		return 1
	}
	if f.keep {
		fmt.Fprintf(os.Stderr, "[chisel-proxy] release copy: %s\n", tmpRelease)
	} else {
		defer func() { _ = cleanupRel() }()
	}

	// A throwaway cache so every cut starts clean: no cross-run contamination.
	cacheDir, err := os.MkdirTemp("", "chisel-proxy-cache-*")
	if err != nil {
		errf(err)
		return 1
	}
	if f.keep {
		fmt.Fprintf(os.Stderr, "[chisel-proxy] cache dir: %s\n", cacheDir)
	} else {
		defer func() { _ = os.RemoveAll(cacheDir) }()
	}

	chiselBin := os.Getenv("CHISEL")
	if chiselBin == "" {
		chiselBin = "chisel"
	}

	cmd := exec.Command(chiselBin, append([]string{"cut", "--release", tmpRelease}, fwd...)...)
	// Pass the full environment through (chisel may rely on more of it in
	// future); our three entries come last so they win over any inherited copy.
	cmd.Env = append(os.Environ(),
		"http_proxy=http://"+addr,
		"HTTP_PROXY=http://"+addr,
		"XDG_CACHE_HOME="+cacheDir,
	)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	runErr := cmd.Run()
	local, upstream := px.Counts()
	fmt.Fprintf(os.Stderr, "[chisel-proxy] served locally: %d   fetched upstream: %d\n", local, upstream)

	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		errf(runErr)
		return 1
	}
	return 0
}
