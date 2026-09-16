package cli

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/lczyk/chisel-proxy/internal/apt"
	"github.com/lczyk/chisel-proxy/internal/bin"
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

	pkgs, bins, sliceFiles, err := buildInputs(pos, deb.Defaults{Arch: arch, Version: f.version})
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

	// Bin packages: pair each to its "bin-" slice and compute the channel track
	// chisel will resolve.
	if len(bins) > 0 {
		// The bin path trusts our interception CA via SSL_CERT_FILE, which Go
		// only honours on Linux; fail early elsewhere instead of a cryptic TLS
		// error.
		if runtime.GOOS != "linux" {
			errf(fmt.Errorf("bin injection is only supported on Linux (it relies on SSL_CERT_FILE, ignored by Go on %s); run chisel-proxy in a Linux container", runtime.GOOS))
			return 1
		}
		// chisel supports bin packages only from format v3; follow that.
		format, err := release.FormatVersion(releaseDir)
		if err != nil {
			errf(fmt.Errorf("cannot read chisel.yaml format from %s: %w", releaseDir, err))
			return 1
		}
		if format < 3 {
			errf(fmt.Errorf("bin injection requires a format v3+ chisel.yaml; %s is format v%d (chisel supports bins only from v3)", releaseDir, format))
			return 1
		}
		releaseName, err := release.ReleaseName(releaseDir)
		if err != nil {
			errf(fmt.Errorf("cannot read release name from %s/chisel.yaml: %w", releaseDir, err))
			return 1
		}
		if err := assignBinTracks(bins, sliceFiles, releaseName); err != nil {
			errf(err)
			return 1
		}
	}

	px := proxy.New(archive, log.New(os.Stderr, "[proxy] ", 0))
	var caFile string
	if len(bins) > 0 {
		store := bin.NewStore(bins)
		caPEM, err := px.EnableMITM(store.Hosts(), store.Handler())
		if err != nil {
			errf(err)
			return 1
		}
		if caFile, err = writeCABundle(caPEM); err != nil {
			errf(err)
			return 1
		}
		if f.keep {
			fmt.Fprintf(os.Stderr, "[chisel-proxy] CA bundle: %s\n", caFile)
		} else {
			defer func() { _ = os.Remove(caFile) }()
		}
	}
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
	// future); our entries come last so they win over any inherited copy.
	env := append(os.Environ(),
		"http_proxy=http://"+addr,
		"HTTP_PROXY=http://"+addr,
		"XDG_CACHE_HOME="+cacheDir,
	)
	if caFile != "" {
		// Bin fetches are HTTPS to the snap store: route them through us and make
		// chisel trust the interception CA.
		env = append(env,
			"https_proxy=http://"+addr,
			"HTTPS_PROXY=http://"+addr,
			"SSL_CERT_FILE="+caFile,
		)
	}
	cmd.Env = env
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

// assignBinTracks pairs each bin to its bin- slice (matched by realname), sets
// the channel track chisel will request ("<default-track>-<release>", risk
// "stable"), and cross-checks that every bin has a slice and every bin slice has
// a payload.
func assignBinTracks(bins []*bin.Bin, sliceFiles []string, releaseName string) error {
	// index bin slice definitions by realname
	sdfTrack := map[string]string{}
	for _, sf := range sliceFiles {
		meta, err := bin.ReadSDFMeta(sf)
		if err != nil {
			continue
		}
		if !strings.HasPrefix(meta.Package, "bin-") {
			continue
		}
		if meta.DefaultTrack == "" {
			return fmt.Errorf("bin slice %s (%s) has no default-track", sf, meta.Package)
		}
		sdfTrack[bin.RealName(meta.Package)] = meta.DefaultTrack
	}

	have := map[string]bool{}
	for _, b := range bins {
		dt, ok := sdfTrack[b.Name]
		if !ok {
			return fmt.Errorf("bin %q has no matching slice (expected a bin-%s.yaml with 'package: bin-%s')", b.Name, b.Name, b.Name)
		}
		b.Track = dt + "-" + releaseName
		b.Risk = "stable"
		have[b.Name] = true
	}
	for rn := range sdfTrack {
		if !have[rn] {
			return fmt.Errorf("bin slice for %q has no payload among the inputs (pass bin-%s/ or %s.tar.xz)", rn, rn, rn)
		}
	}
	return nil
}

// writeCABundle writes the system CA bundle (so real TLS still verifies)
// followed by the proxy's ephemeral CA to a temp file, for SSL_CERT_FILE.
func writeCABundle(caPEM []byte) (string, error) {
	var buf []byte
	for _, p := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt"} {
		if b, err := os.ReadFile(p); err == nil {
			buf = append(buf, b...)
			buf = append(buf, '\n')
			break
		}
	}
	buf = append(buf, caPEM...)
	f, err := os.CreateTemp("", "chisel-proxy-ca-*.pem")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return "", err
	}
	return f.Name(), f.Close()
}
