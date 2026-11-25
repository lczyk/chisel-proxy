package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lczyk/assert"
)

func writeFile(t *testing.T, p, content string, mode os.FileMode) {
	t.Helper()
	assert.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
	assert.NoError(t, os.WriteFile(p, []byte(content), mode))
}

func TestFromDirFabricatesAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "pkg")
	writeFile(t, filepath.Join(pkgDir, "usr", "bin", "demo"), "#!/bin/sh\necho hi\n", 0755)
	writeFile(t, filepath.Join(pkgDir, "usr", "share", "doc", "demo", "README"), "hello\n", 0644)

	pkg, err := FromDir(pkgDir, Defaults{Arch: "amd64", Version: "1.2"})
	assert.NoError(t, err)
	assert.Equal(t, pkg.Name, "pkg")
	assert.Equal(t, pkg.Version, "1.2")
	assert.Equal(t, pkg.Arch, "amd64")
	want := []string{"/usr/bin/demo", "/usr/share/doc/demo/README"}
	assert.EqualArrays(t, pkg.Files, want)

	debPath := filepath.Join(dir, "out.deb")
	assert.NoError(t, os.WriteFile(debPath, pkg.Data, 0644))
	got, err := FromFile(debPath)
	assert.NoError(t, err)
	assert.Equal(t, got.Name, pkg.Name)
	assert.Equal(t, got.Version, pkg.Version)
	assert.Equal(t, got.Arch, pkg.Arch)
	assert.EqualArrays(t, got.Files, want)
	assert.Equal(t, got.SHA256, pkg.SHA256)
}

func TestFromDirHonoursControlAndSkipsDEBIAN(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "p")
	writeFile(t, filepath.Join(pkgDir, "DEBIAN", "control"),
		"Package: custom\nVersion: 9.9\nArchitecture: arm64\nMaintainer: x\nDescription: y\n", 0644)
	writeFile(t, filepath.Join(pkgDir, "usr", "bin", "x"), "x", 0755)

	pkg, err := FromDir(pkgDir, Defaults{Arch: "amd64", Version: "0.0.0"})
	assert.NoError(t, err)
	assert.Equal(t, pkg.Name, "custom")
	assert.Equal(t, pkg.Version, "9.9")
	assert.Equal(t, pkg.Arch, "arm64")
	for _, f := range pkg.Files {
		assert.That(t, !strings.HasPrefix(f, "/DEBIAN"), "DEBIAN leaked into payload: "+f)
	}
	assert.EqualArrays(t, pkg.Files, []string{"/usr/bin/x"})
}

func TestInferArch(t *testing.T) {
	a, err := InferArch()
	assert.NoError(t, err)
	assert.That(t, ValidArch(a), "InferArch returned invalid arch: "+a)
}

func TestArchesAndCPUs(t *testing.T) {
	all := Arches()
	assert.EqualArrays(t, all, []string{"amd64", "arm64", "armhf", "i386", "ppc64el", "riscv64", "s390x"})
	for _, a := range all {
		assert.That(t, ValidArch(a), a)
		assert.That(t, len(CPUs(a)) > 0, a)
	}
	for _, a := range archMap {
		assert.That(t, ValidArch(a), a)
	}
	assert.That(t, !ValidArch("x86"))
	assert.EqualArrays(t, CPUs("amd64"), []string{"x86_64"})
	assert.EqualArrays(t, CPUs("i386"), []string{"i386", "i686"})
	assert.Len(t, CPUs("x86"), 0)

	// Copies: a caller cannot change the tables.
	all[0] = "nope"
	assert.Equal(t, Arches()[0], "amd64")
}

// A hard link is a path the package installs, as much as the file it links to.
func TestFromFileListsHardLinks(t *testing.T) {
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	assert.NoError(t, tw.WriteHeader(&tar.Header{Name: "./usr/bin/a", Typeflag: tar.TypeReg, Mode: 0755, Size: 1}))
	_, err := tw.Write([]byte("x"))
	assert.NoError(t, err)
	assert.NoError(t, tw.WriteHeader(&tar.Header{Name: "./usr/bin/b", Typeflag: tar.TypeLink, Linkname: "./usr/bin/a", Mode: 0755}))
	assert.NoError(t, tw.Close())
	assert.NoError(t, gz.Close())
	control, err := buildControlTar(fabricateControl("demo", "1.0", "amd64"))
	assert.NoError(t, err)
	raw, err := buildAr(control, data.Bytes())
	assert.NoError(t, err)
	p := filepath.Join(t.TempDir(), "demo.deb")
	assert.NoError(t, os.WriteFile(p, raw, 0644))

	pkg, err := FromFile(p)
	assert.NoError(t, err)
	assert.EqualArrays(t, pkg.Files, []string{"/usr/bin/a", "/usr/bin/b"})
}
