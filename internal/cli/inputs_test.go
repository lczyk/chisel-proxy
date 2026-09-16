package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lczyk/assert"
	"github.com/lczyk/chisel-proxy/internal/deb"
)

func TestBuildInputsClassifies(t *testing.T) {
	dir := t.TempDir()

	// a deb payload dir
	pkgDir := filepath.Join(dir, "demo")
	assert.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo"), []byte("x"), 0755))

	// a bin payload dir (bin- prefix)
	binDir := filepath.Join(dir, "bin-alpha")
	assert.NoError(t, os.MkdirAll(filepath.Join(binDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(binDir, "usr", "bin", "alpha"), []byte("x"), 0755))

	// a prebuilt bin tarball
	tarxz := filepath.Join(dir, "beta.tar.xz")
	assert.NoError(t, os.WriteFile(tarxz, []byte("not-really-xz-but-fine-for-classification"), 0644))

	// a slice
	slice := filepath.Join(dir, "demo.yaml")
	assert.NoError(t, os.WriteFile(slice, []byte("package: demo\n"), 0644))

	pkgs, bins, slices, err := buildInputs([]string{pkgDir, binDir, tarxz, slice},
		deb.Defaults{Arch: "amd64", Version: "1.0"})
	assert.NoError(t, err)
	assert.Len(t, pkgs, 1)
	assert.Equal(t, pkgs[0].Name, "demo")
	assert.Len(t, bins, 2)
	assert.Equal(t, bins[0].Name, "alpha")
	assert.Equal(t, bins[1].Name, "beta")
	assert.EqualArrays(t, slices, []string{slice})
}

func TestBuildInputsRejectsTarGz(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.tar.gz")
	assert.NoError(t, os.WriteFile(f, []byte("x"), 0644))
	_, _, _, err := buildInputs([]string{f}, deb.Defaults{})
	assert.Error(t, err, assert.AnyError)
}

func TestBuildInputsRejectsUnknownExtension(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.txt")
	assert.NoError(t, os.WriteFile(f, []byte("x"), 0644))
	_, _, _, err := buildInputs([]string{f}, deb.Defaults{})
	assert.Error(t, err, assert.AnyError)
}

func TestBinRealName(t *testing.T) {
	assert.Equal(t, binRealName("bin-foo"), "foo")
	assert.Equal(t, binRealName("foo.tar.xz"), "foo")
	assert.Equal(t, binRealName("bin-foo.tar.xz"), "foo")
	assert.Equal(t, binRealName("demo"), "demo")
}
