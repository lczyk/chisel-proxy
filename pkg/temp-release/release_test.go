package temprelease

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lczyk/assert"
)

const chiselYAML = "format: v2\n\narchives:\n  ubuntu:\n    version: 24.04\n    components: [main]\n    suites: [noble]\n"

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	assert.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
	assert.NoError(t, os.WriteFile(p, []byte(content), 0644))
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	assert.NoError(t, err)
	return string(b)
}

func testCheckout(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	writeFile(t, filepath.Join(src, "chisel.yaml"), chiselYAML)
	writeFile(t, filepath.Join(src, "slices", "hello.yaml"), "package: hello\n")
	writeFile(t, filepath.Join(src, ".git", "HEAD"), "ref: refs/heads/main\n")
	assert.NoError(t, os.Symlink("hello.yaml", filepath.Join(src, "slices", "link.yaml")))
	return src
}

func TestCopySkipsGitAndKeepsLinks(t *testing.T) {
	src := testCheckout(t)
	r, err := Copy(src)
	assert.NoError(t, err)
	defer func() { _ = r.Remove() }()

	assert.NotEqual(t, r.Dir, src)
	assert.Equal(t, readFile(t, filepath.Join(r.Dir, "chisel.yaml")), chiselYAML)
	assert.Equal(t, readFile(t, filepath.Join(r.Dir, "slices", "hello.yaml")), "package: hello\n")
	_, err = os.Stat(filepath.Join(r.Dir, ".git"))
	assert.That(t, os.IsNotExist(err), ".git copied into the release")
	fi, err := os.Lstat(filepath.Join(r.Dir, "slices", "link.yaml"))
	assert.NoError(t, err)
	assert.That(t, fi.Mode()&os.ModeSymlink != 0, "symlink copied as a regular file")
}

func TestCopyRejectsNonRelease(t *testing.T) {
	_, err := Copy(t.TempDir())
	assert.Error(t, err, "is not a chisel release")
}

func TestNewAndWrite(t *testing.T) {
	r, err := New([]byte(chiselYAML))
	assert.NoError(t, err)
	defer func() { _ = r.Remove() }()

	assert.Equal(t, readFile(t, filepath.Join(r.Dir, "chisel.yaml")), chiselYAML)
	fi, err := os.Stat(filepath.Join(r.Dir, "slices"))
	assert.NoError(t, err)
	assert.That(t, fi.IsDir(), "slices/ is not a directory")

	assert.NoError(t, r.WriteSlice("demo", []byte("package: demo\n")))
	assert.Equal(t, readFile(t, filepath.Join(r.Dir, "slices", "demo.yaml")), "package: demo\n")

	assert.NoError(t, r.WriteChiselYAML([]byte("format: v3\n")))
	assert.Equal(t, readFile(t, filepath.Join(r.Dir, "chisel.yaml")), "format: v3\n")
}

func TestAddSliceOverwritesByBasename(t *testing.T) {
	src := testCheckout(t)
	r, err := Copy(src)
	assert.NoError(t, err)
	defer func() { _ = r.Remove() }()

	outside := filepath.Join(t.TempDir(), "hello.yaml")
	writeFile(t, outside, "package: hello\nslices:\n  bins:\n")
	assert.NoError(t, r.AddSlice(outside))

	assert.Equal(t, readFile(t, filepath.Join(r.Dir, "slices", "hello.yaml")), "package: hello\nslices:\n  bins:\n")
	// the source checkout keeps its own copy
	assert.Equal(t, readFile(t, filepath.Join(src, "slices", "hello.yaml")), "package: hello\n")

	assert.Error(t, r.AddSlice(filepath.Join(t.TempDir(), "missing.yaml")), "cannot splice slice")
}

func TestRemove(t *testing.T) {
	r, err := New([]byte(chiselYAML))
	assert.NoError(t, err)
	assert.NoError(t, r.Remove())
	_, err = os.Stat(r.Dir)
	assert.That(t, os.IsNotExist(err), "release dir still present after Remove")
}
