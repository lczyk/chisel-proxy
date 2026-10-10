package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/lczyk/chisel-proxy/internal/proxy"
	"github.com/lczyk/chisel-proxy/internal/release"
	"github.com/lczyk/chisel-proxy/pkg/apt"
	"github.com/lczyk/chisel-proxy/pkg/deb"
)

// ownedFlags are the chisel flags chisel-proxy takes before '--' and forwards
// itself, each with why chisel must not get it straight from the user.
var ownedFlags = []struct{ name, why string }{
	{"release", "it points chisel at the injected temp copy"},
	{"arch", "it also sets the architecture of the served archive"},
}

// verbFlags lists which owned flags each chisel verb accepts.
var verbFlags = map[string]map[string]bool{
	"cut":  {"release": true, "arch": true},
	"info": {"release": true},
}

// checkOwnedFlags rejects owned flags after '--', and owned flags the chisel
// verb itself would not accept.
func checkOwnedFlags(verb string, f flags, fwd []string) error {
	accepts := verbFlags[verb]
	if f.arch != "" && !accepts["arch"] {
		return fmt.Errorf("chisel %s has no --arch", verb)
	}
	for _, a := range fwd {
		for _, o := range ownedFlags {
			if a != "--"+o.name && !strings.HasPrefix(a, "--"+o.name+"=") {
				continue
			}
			if !accepts[o.name] {
				return fmt.Errorf("chisel %s has no --%s", verb, o.name)
			}
			return fmt.Errorf("--%s is chisel-proxy's own flag; put it before '--', not after (%s)", o.name, o.why)
		}
	}
	return nil
}

// runChisel runs "chisel <verb> --release <temp-copy> [--arch <a>] <forwarded
// args>" against the full setup: proxy archive, injected release copy, spliced
// slices and a throwaway cache. --arch is forwarded only when given; otherwise
// chisel picks its own default, which matches the inferred archive
// architecture. The setup never depends on verb, so every verb sees exactly
// what cut sees.
func runChisel(verb string, args []string) int {
	f, pos, fwd, err := parseArgs(args, true)
	if err != nil {
		errf(err)
		return 2
	}
	if len(fwd) == 0 {
		example := "foo_bins"
		if verb == "cut" {
			example += " --root ./rootfs"
		}
		errf(fmt.Errorf("%s needs chisel arguments after '--', e.g.:\n"+
			"  chisel-proxy %s ./foo.deb -- %s", verb, verb, example))
		return 2
	}
	if err := checkOwnedFlags(verb, f, fwd); err != nil {
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
	releaseDir := f.release
	if releaseDir == "" {
		releaseDir = "."
	}

	pkgs, sliceFiles, err := buildInputs(pos, deb.Defaults{Arch: arch, Version: f.version})
	if err != nil {
		errf(err)
		return 1
	}

	archive, err := apt.New(name, arch, pkgs)
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

	tmpRelease, cleanupRel, err := release.Inject(releaseDir, archive, f.version, sliceFiles)
	if err != nil {
		errf(err)
		return 1
	}
	if f.keep {
		fmt.Fprintf(os.Stderr, "[chisel-proxy] release copy: %s\n", tmpRelease)
	} else {
		defer func() { _ = cleanupRel() }()
	}

	// A throwaway cache so every run starts clean: no cross-run contamination.
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

	chiselArgs := []string{verb, "--release", tmpRelease}
	if f.arch != "" {
		chiselArgs = append(chiselArgs, "--arch", arch)
	}
	cmd := exec.Command(chiselBin, append(chiselArgs, fwd...)...)
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
