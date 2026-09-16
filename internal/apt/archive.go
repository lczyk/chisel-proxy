// Package apt synthesises an in-memory apt archive for a single synthetic suite:
// a signed InRelease, a gzipped Packages index, and the input .debs. It serves
// them for the archive.ubuntu.com paths chisel requests through the proxy. The
// exact fields and paths here are dictated by chisel's internal/archive reader.
package apt

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/lczyk/chisel-proxy/internal/deb"
	"github.com/lczyk/chisel-proxy/internal/sign"
)

// Archive is the synthesised, signed archive for one suite.
type Archive struct {
	Suite     string // used as suite, codename and component alike
	Component string
	Arch      string

	pkgs   []*deb.Package
	byPool map[string]*deb.Package // pool-relative path -> package

	inRelease   []byte
	packagesRaw []byte
	packagesGz  []byte
}

// New synthesises and signs the archive. suite names the suite/codename and the
// single component.
func New(suite, arch string, pkgs []*deb.Package, signer *sign.Signer) (*Archive, error) {
	// An empty archive is valid: a slice-only run still injects the archive and
	// passes everything through to upstream.
	a := &Archive{
		Suite:     suite,
		Component: suite,
		Arch:      arch,
		pkgs:      pkgs,
		byPool:    make(map[string]*deb.Package, len(pkgs)),
	}
	for _, p := range pkgs {
		pp := a.poolPath(p)
		if _, dup := a.byPool[pp]; dup {
			return nil, fmt.Errorf("duplicate package %s_%s_%s: two inputs map to the same pool file", p.Name, p.Version, p.Arch)
		}
		a.byPool[pp] = p
	}
	a.packagesRaw = a.buildPackages()
	gz, err := gzipBytes(a.packagesRaw)
	if err != nil {
		return nil, err
	}
	a.packagesGz = gz
	inRel, err := signer.ClearSign([]byte(a.buildRelease()))
	if err != nil {
		return nil, fmt.Errorf("cannot sign InRelease: %w", err)
	}
	a.inRelease = inRel
	return a, nil
}

// Packages returns the input packages.
func (a *Archive) Packages() []*deb.Package { return a.pkgs }

func (a *Archive) poolPath(p *deb.Package) string {
	return fmt.Sprintf("pool/%s/%s_%s_%s.deb", a.Suite, p.Name, p.Version, p.Arch)
}

func (a *Archive) buildPackages() []byte {
	var b strings.Builder
	for i, p := range a.pkgs {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "Package: %s\n", p.Name)
		fmt.Fprintf(&b, "Version: %s\n", p.Version)
		fmt.Fprintf(&b, "Architecture: %s\n", p.Arch)
		b.WriteString("Maintainer: chisel-proxy <chisel-proxy@localhost>\n")
		fmt.Fprintf(&b, "Filename: %s\n", a.poolPath(p))
		fmt.Fprintf(&b, "Size: %d\n", p.Size)
		fmt.Fprintf(&b, "SHA256: %s\n", p.SHA256)
		b.WriteString("Description: Served locally by chisel-proxy.\n")
	}
	return []byte(b.String())
}

func (a *Archive) buildRelease() string {
	pkgPath := fmt.Sprintf("%s/binary-%s/Packages", a.Component, a.Arch)
	gzPath := pkgPath + ".gz"

	var b strings.Builder
	// Label: Ubuntu is structural: chisel parses InRelease with
	// control.ParseString("Label", body) and looks up the "Ubuntu" section.
	b.WriteString("Label: Ubuntu\n")
	fmt.Fprintf(&b, "Suite: %s\n", a.Suite)
	fmt.Fprintf(&b, "Codename: %s\n", a.Suite)
	fmt.Fprintf(&b, "Components: %s\n", a.Component)
	fmt.Fprintf(&b, "Architectures: %s\n", a.Arch)
	fmt.Fprintf(&b, "Date: %s\n", time.Now().UTC().Format(time.RFC1123))
	// chisel looks up the checksum of the *uncompressed* Packages path even
	// though it fetches Packages.gz. Digests must be lowercase hex.
	b.WriteString("SHA256:\n")
	fmt.Fprintf(&b, " %s %d %s\n", sha256hex(a.packagesRaw), len(a.packagesRaw), pkgPath)
	fmt.Fprintf(&b, " %s %d %s\n", sha256hex(a.packagesGz), len(a.packagesGz), gzPath)
	b.WriteString("SHA512:\n")
	fmt.Fprintf(&b, " %s %d %s\n", sha512hex(a.packagesRaw), len(a.packagesRaw), pkgPath)
	fmt.Fprintf(&b, " %s %d %s\n", sha512hex(a.packagesGz), len(a.packagesGz), gzPath)
	return b.String()
}

// Match reports whether path targets this archive's suite.
func (a *Archive) Match(path string) bool {
	return strings.Contains(path, "/dists/"+a.Suite+"/") ||
		strings.Contains(path, "/pool/"+a.Suite+"/")
}

// Serve returns the body for a matched path. ok is false for a path within the
// suite that has no content (a local 404).
func (a *Archive) Serve(path string) (body []byte, contentType string, ok bool) {
	if marker := "/dists/" + a.Suite + "/"; strings.Contains(path, marker) {
		tail := path[strings.Index(path, marker)+len(marker):]
		switch tail {
		case "InRelease":
			return a.inRelease, "text/plain", true
		case a.Component + "/binary-" + a.Arch + "/Packages.gz":
			return a.packagesGz, "application/gzip", true
		case a.Component + "/binary-" + a.Arch + "/Packages":
			return a.packagesRaw, "text/plain", true
		}
		return nil, "", false
	}
	if marker := "/pool/" + a.Suite + "/"; strings.Contains(path, marker) {
		rel := "pool/" + a.Suite + "/" + path[strings.Index(path, marker)+len(marker):]
		if p, found := a.byPool[rel]; found {
			return p.Data, "application/vnd.debian.binary-package", true
		}
	}
	return nil, "", false
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func sha256hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func sha512hex(b []byte) string {
	s := sha512.Sum512(b)
	return hex.EncodeToString(s[:])
}
