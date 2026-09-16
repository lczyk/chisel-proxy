// Package cli parses arguments and runs the chisel-proxy verbs.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lczyk/chisel-proxy/internal/bin"
	"github.com/lczyk/chisel-proxy/internal/deb"
	vinfo "github.com/lczyk/chisel-proxy/src/version"
	ver "github.com/lczyk/version/go"
)

const defaultName = "chisel-proxy"

// Main runs the CLI and returns a process exit code.
func Main(args []string) int {
	if len(args) == 0 {
		usage(os.Stderr)
		return 2
	}
	switch args[0] {
	case "cut":
		return runCut(args[1:])
	case "serve":
		return runServe(args[1:])
	case "version", "--version", "-v":
		fmt.Println(ver.FormatVersion(vinfo.Version, vinfo.CommitSHA, vinfo.BuildDate, vinfo.BuildInfo))
		return 0
	case "help", "--help", "-h":
		usage(os.Stdout)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "chisel-proxy: unknown command %q\n\n", args[0])
		usage(os.Stderr)
		return 2
	}
}

type flags struct {
	port    int
	name    string
	arch    string
	version string
	release string
	keep    bool
}

// parseArgs pulls recognised flags and positionals out of args. When forCut is
// true, a "--" token ends flag/positional parsing and everything after it is
// returned as forwarded arguments.
func parseArgs(args []string, forCut bool) (f flags, pos, fwd []string, err error) {
	i := 0
	for i < len(args) {
		a := args[i]
		if forCut && a == "--" {
			fwd = append(fwd, args[i+1:]...)
			return f, pos, fwd, nil
		}
		if strings.HasPrefix(a, "--") {
			key := a[2:]
			val := ""
			hasVal := false
			if j := strings.IndexByte(key, '='); j >= 0 {
				val, key, hasVal = key[j+1:], key[:j], true
			}
			need := func() (string, error) {
				if hasVal {
					return val, nil
				}
				if i+1 >= len(args) {
					return "", fmt.Errorf("flag --%s needs a value", key)
				}
				i++
				return args[i], nil
			}
			switch key {
			case "port":
				v, e := need()
				if e != nil {
					return f, nil, nil, e
				}
				n, e := strconv.Atoi(v)
				if e != nil {
					return f, nil, nil, fmt.Errorf("--port: %v", e)
				}
				f.port = n
			case "name":
				if f.name, err = need(); err != nil {
					return f, nil, nil, err
				}
			case "arch":
				if f.arch, err = need(); err != nil {
					return f, nil, nil, err
				}
			case "version":
				if f.version, err = need(); err != nil {
					return f, nil, nil, err
				}
			case "release":
				if f.release, err = need(); err != nil {
					return f, nil, nil, err
				}
			case "keep":
				f.keep = true
			default:
				return f, nil, nil, fmt.Errorf("unknown flag --%s", key)
			}
			i++
			continue
		}
		pos = append(pos, a)
		i++
	}
	return f, pos, fwd, nil
}

// buildInputs classifies each path by kind, following chisel's own bin- naming
// convention:
//   - a bin-*/ directory        -> packed as a bin package
//   - any other directory       -> packed as a deb package
//   - a .deb file               -> deb package, served as-is
//   - a .tar.xz file            -> bin package, served as-is
//   - a .yaml/.yml file         -> slice definition, spliced into the release
func buildInputs(paths []string, def deb.Defaults) (pkgs []*deb.Package, bins []*bin.Bin, slices []string, err error) {
	if len(paths) == 0 {
		return nil, nil, nil, fmt.Errorf("no inputs given")
	}
	binDef := func(base string) bin.Defaults {
		return bin.Defaults{Name: binRealName(base), Arch: def.Arch, Version: def.Version}
	}
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, nil, nil, err
		}
		base := filepath.Base(p)
		if fi.IsDir() {
			if strings.HasPrefix(base, "bin-") {
				b, err := bin.FromDir(p, binDef(base))
				if err != nil {
					return nil, nil, nil, err
				}
				bins = append(bins, b)
			} else {
				pkg, err := deb.FromDir(p, def)
				if err != nil {
					return nil, nil, nil, err
				}
				pkgs = append(pkgs, pkg)
			}
			continue
		}
		switch {
		case strings.HasSuffix(base, ".deb"):
			pkg, err := deb.FromFile(p)
			if err != nil {
				return nil, nil, nil, err
			}
			pkgs = append(pkgs, pkg)
		case strings.HasSuffix(base, ".tar.xz"):
			b, err := bin.FromTarXZ(p, binDef(base))
			if err != nil {
				return nil, nil, nil, err
			}
			bins = append(bins, b)
		case strings.HasSuffix(base, ".yaml"), strings.HasSuffix(base, ".yml"):
			slices = append(slices, p)
		case strings.HasSuffix(base, ".tar.gz"):
			return nil, nil, nil, fmt.Errorf("%s: chisel bins are .tar.xz, not .tar.gz", p)
		default:
			return nil, nil, nil, fmt.Errorf("%s: not a .deb, .tar.xz, .yaml, or a directory", p)
		}
	}
	return pkgs, bins, slices, nil
}

// binRealName strips the bin- prefix and any .tar.xz suffix from a payload
// basename to get the store realname (e.g. "bin-foo" or "foo.tar.xz" -> "foo").
func binRealName(base string) string {
	return strings.TrimPrefix(strings.TrimSuffix(base, ".tar.xz"), "bin-")
}

func resolveArch(a string) (string, error) {
	if a != "" {
		if !deb.ValidArch(a) {
			return "", fmt.Errorf("invalid architecture %q", a)
		}
		return a, nil
	}
	return deb.InferArch()
}

func errf(err error) {
	fmt.Fprintf(os.Stderr, "chisel-proxy: %v\n", err)
}

func usage(w io.Writer) {
	fmt.Fprint(w, `chisel-proxy - serve your own .debs to chisel, no upload needed

usage:
  chisel-proxy cut   [flags] <deb|dir|slice.yaml>... -- <chisel cut args>
  chisel-proxy serve [flags] <deb|dir>...
  chisel-proxy version

each positional is one of:
  <deb>          a .deb file, served as-is
  <dir>          a directory, packed into a .deb (honouring DEBIAN/control if present)
  bin-<name>/    a directory, packed into a bin package (cut only)
  <name>.tar.xz  a prebuilt bin tarball, served as-is (cut only)
  <slice.yaml>   a slice definition, spliced into the release checkout (cut only)

a bin package is served to chisel over a TLS-intercepted snap store and needs a
matching bin-<name>.yaml slice among the inputs and a format v3+ release. works
on Linux, and on macOS with a chisel built with Go 1.27+ (SSL_CERT_FILE).

flags:
  --release <dir>   chisel-releases checkout to inject into (cut; default ".")
  --port <n>        proxy port (default: auto-pick a free one)
  --name <s>        suite/component/archive name (default "chisel-proxy")
  --arch <a>        Debian architecture (default: infer from host)
  --version <v>     packed-deb version / archive version (default "0.0.0" / from release)
  --keep            keep the temp release copy and cache dir (cut)

cut runs "chisel cut --release <temp-copy> <your args>" with http_proxy pointed
at the proxy and a throwaway cache, then passes chisel's exit code through.
serve brings the proxy up and prints the chisel.yaml blocks to paste yourself.
`)
}
