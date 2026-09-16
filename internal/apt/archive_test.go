package apt

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lczyk/assert"
	"github.com/lczyk/chisel-proxy/internal/deb"
	"github.com/lczyk/chisel-proxy/internal/sign"
)

func testPackage(t *testing.T) *deb.Package {
	t.Helper()
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "demo")
	assert.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo"), []byte("hi"), 0755))
	p, err := deb.FromDir(pkgDir, deb.Defaults{Arch: "amd64", Version: "1.0"})
	assert.NoError(t, err)
	return p
}

func testArchive(t *testing.T) *Archive {
	t.Helper()
	signer, err := sign.New("t", "t@example.invalid")
	assert.NoError(t, err)
	a, err := New("chisel-proxy", "amd64", []*deb.Package{testPackage(t)}, signer)
	assert.NoError(t, err)
	return a
}

// pathInfoExp mirrors chisel's control.ParsePathInfo checksum-row regex.
var pathInfoExp = regexp.MustCompile(`([a-f0-9]{32,}) +([0-9]+) +(\S+)`)

func TestReleaseChecksumTableMatchesPackages(t *testing.T) {
	a := testArchive(t)
	rel := a.buildRelease()

	assert.ContainsString(t, rel, "Label: Ubuntu\n")
	assert.ContainsString(t, rel, "Architectures: amd64\n")
	assert.ContainsString(t, rel, "Components: chisel-proxy\n")

	// The uncompressed Packages digest chisel looks up must equal sha256 of the
	// actual Packages bytes, and be lowercase hex.
	wantPath := "chisel-proxy/binary-amd64/Packages"
	var digest string
	for _, line := range strings.Split(rel, "\n") {
		m := pathInfoExp.FindStringSubmatch(strings.TrimSpace(line))
		if m != nil && m[3] == wantPath && len(m[1]) == 64 {
			digest = m[1]
			break
		}
	}
	assert.That(t, digest != "", "no SHA256 row for "+wantPath+" in Release")
	assert.Equal(t, digest, sha256hex(a.packagesRaw))
}

func TestServeRoutes(t *testing.T) {
	a := testArchive(t)
	base := "http://archive.ubuntu.com/ubuntu"

	body, _, ok := a.Serve(base + "/dists/chisel-proxy/InRelease")
	assert.That(t, ok, "InRelease not served")
	assert.ContainsString(t, string(body), "BEGIN PGP SIGNED MESSAGE")

	gzBody, _, ok := a.Serve(base + "/dists/chisel-proxy/chisel-proxy/binary-amd64/Packages.gz")
	assert.That(t, ok, "Packages.gz not served")
	zr, err := gzip.NewReader(bytes.NewReader(gzBody))
	assert.NoError(t, err)
	raw, err := io.ReadAll(zr)
	assert.NoError(t, err)
	assert.EqualArrays(t, raw, a.packagesRaw)
	assert.ContainsString(t, string(raw), "Filename: pool/chisel-proxy/demo_1.0_amd64.deb")

	debBody, _, ok := a.Serve(base + "/pool/chisel-proxy/demo_1.0_amd64.deb")
	assert.That(t, ok, "pool deb not served")
	assert.EqualArrays(t, debBody, a.pkgs[0].Data)

	_, _, ok = a.Serve(base + "/dists/chisel-proxy/nope")
	assert.That(t, !ok, "unknown suite path should not be served")
	assert.That(t, !a.Match(base+"/dists/noble/InRelease"), "Match should be false for a foreign suite")
}

func TestNewRejectsDuplicatePackages(t *testing.T) {
	p := testPackage(t)
	signer, err := sign.New("t", "t@example.invalid")
	assert.NoError(t, err)
	_, err = New("chisel-proxy", "amd64", []*deb.Package{p, p}, signer)
	assert.Error(t, err, assert.AnyError)
}

func TestNewEmptyArchive(t *testing.T) {
	signer, err := sign.New("t", "t@example.invalid")
	assert.NoError(t, err)
	a, err := New("chisel-proxy", "amd64", nil, signer)
	assert.NoError(t, err)
	body, _, ok := a.Serve("http://archive.ubuntu.com/ubuntu/dists/chisel-proxy/InRelease")
	assert.That(t, ok, "empty archive should still serve InRelease")
	assert.ContainsString(t, string(body), "BEGIN PGP SIGNED MESSAGE")
}
