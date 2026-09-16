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
	pkgDir := filepath.Join(dir, "demo")
	assert.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo"), []byte("x"), 0755))
	slice := filepath.Join(dir, "demo.yaml")
	assert.NoError(t, os.WriteFile(slice, []byte("package: demo\n"), 0644))

	pkgs, slices, err := buildInputs([]string{pkgDir, slice}, deb.Defaults{Arch: "amd64", Version: "1.0"})
	assert.NoError(t, err)
	assert.Len(t, pkgs, 1)
	assert.EqualArrays(t, slices, []string{slice})
}

func TestBuildInputsRejectsUnknownExtension(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x.txt")
	assert.NoError(t, os.WriteFile(f, []byte("x"), 0644))
	_, _, err := buildInputs([]string{f}, deb.Defaults{})
	assert.Error(t, err, assert.AnyError)
}
