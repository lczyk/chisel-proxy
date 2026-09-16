package release

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lczyk/assert"
	"gopkg.in/yaml.v3"

	"github.com/lczyk/chisel-proxy/internal/apt"
	"github.com/lczyk/chisel-proxy/internal/deb"
	"github.com/lczyk/chisel-proxy/internal/sign"
)

type yArch struct {
	Version    string   `yaml:"version"`
	Priority   *int     `yaml:"priority"`
	Components []string `yaml:"components"`
	Suites     []string `yaml:"suites"`
	PublicKeys []string `yaml:"public-keys"`
}

type yPub struct {
	ID    string `yaml:"id"`
	Armor string `yaml:"armor"`
}

type yDoc struct {
	Format     string           `yaml:"format"`
	Archives   map[string]yArch `yaml:"archives"`
	PublicKeys map[string]yPub  `yaml:"public-keys"`
}

func testArchiveAndSigner(t *testing.T) (*apt.Archive, *sign.Signer) {
	t.Helper()
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "demo")
	assert.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo"), []byte("hi"), 0755))
	p, err := deb.FromDir(pkgDir, deb.Defaults{Arch: "amd64", Version: "1.0"})
	assert.NoError(t, err)
	signer, err := sign.New("t", "t@example.invalid")
	assert.NoError(t, err)
	a, err := apt.New("chisel-proxy", "amd64", []*deb.Package{p}, signer)
	assert.NoError(t, err)
	return a, signer
}

func TestPatchYAMLInjectsArchiveAndKey(t *testing.T) {
	// One archive with an explicit priority, one without: exercises both the
	// "keep existing" and "assign missing" branches.
	src := `format: v2

archives:
  ubuntu:
    version: 24.04
    components: [main, universe]
    suites: [noble]
    priority: 20
    public-keys: [ubuntu-key]
  extra:
    version: 24.04
    components: [main]
    suites: [noble]
    public-keys: [ubuntu-key]

public-keys:
  ubuntu-key:
    id: "0000000000000000"
    armor: |
      placeholder
`
	a, signer := testArchiveAndSigner(t)
	armor, err := signer.ArmoredPublicKey()
	assert.NoError(t, err)
	out, err := patchYAML([]byte(src), a, signer.KeyID(), armor, "")
	assert.NoError(t, err)

	var doc yDoc
	assert.NoError(t, yaml.Unmarshal(out, &doc))

	ours, ok := doc.Archives["chisel-proxy"]
	assert.That(t, ok, "proxy archive not added")
	assert.Equal(t, ours.Version, "24.04")
	assert.NotNil(t, ours.Priority, "proxy archive has no priority")

	// Every archive must now have a unique priority, and ours must be lowest.
	seen := map[int]bool{}
	lowest := 1 << 30
	for name, arch := range doc.Archives {
		assert.NotNil(t, arch.Priority, "archive "+name+" has no priority after patch")
		assert.That(t, !seen[*arch.Priority], "duplicate priority")
		seen[*arch.Priority] = true
		if *arch.Priority < lowest {
			lowest = *arch.Priority
		}
	}
	assert.Equal(t, *ours.Priority, lowest)

	key, ok := doc.PublicKeys["chisel-proxy-key"]
	assert.That(t, ok, "proxy public key not added")
	assert.Equal(t, key.ID, signer.KeyID())
	assert.ContainsString(t, key.Armor, "BEGIN PGP PUBLIC KEY BLOCK")
}

func TestInjectCopiesTreeAndLeavesSourceUntouched(t *testing.T) {
	src := t.TempDir()
	original := `format: v2

archives:
  ubuntu:
    version: 24.04
    components: [main]
    suites: [noble]
    priority: 20
    public-keys: [ubuntu-key]

public-keys:
  ubuntu-key:
    id: "0000000000000000"
    armor: |
      placeholder
`
	assert.NoError(t, os.WriteFile(filepath.Join(src, "chisel.yaml"), []byte(original), 0644))
	assert.NoError(t, os.MkdirAll(filepath.Join(src, "slices"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(src, "slices", "hello.yaml"), []byte("package: hello\n"), 0644))

	a, signer := testArchiveAndSigner(t)
	dir, cleanup, err := Inject(src, a, signer, "", nil)
	assert.NoError(t, err)
	defer func() { _ = cleanup() }()

	got, _ := os.ReadFile(filepath.Join(src, "chisel.yaml"))
	assert.Equal(t, string(got), original)

	copied, _ := os.ReadFile(filepath.Join(dir, "chisel.yaml"))
	assert.ContainsString(t, string(copied), "chisel-proxy:")

	_, err = os.Stat(filepath.Join(dir, "slices", "hello.yaml"))
	assert.NoError(t, err)
}

func TestInjectSplicesSliceFiles(t *testing.T) {
	src := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(src, "chisel.yaml"), []byte(
		"format: v2\n\narchives:\n  ubuntu:\n    version: 24.04\n    components: [main]\n    suites: [noble]\n    priority: 20\n    public-keys: [ubuntu-key]\n\npublic-keys:\n  ubuntu-key:\n    id: \"0000000000000000\"\n    armor: |\n      placeholder\n"), 0644))
	assert.NoError(t, os.MkdirAll(filepath.Join(src, "slices"), 0755))

	// a slice file living outside the checkout, to be spliced in by basename
	sliceSrc := filepath.Join(t.TempDir(), "demo-hello.yaml")
	assert.NoError(t, os.WriteFile(sliceSrc, []byte("package: demo-hello\n"), 0644))

	a, signer := testArchiveAndSigner(t)
	dir, cleanup, err := Inject(src, a, signer, "", []string{sliceSrc})
	assert.NoError(t, err)
	defer func() { _ = cleanup() }()

	spliced, err := os.ReadFile(filepath.Join(dir, "slices", "demo-hello.yaml"))
	assert.NoError(t, err)
	assert.Equal(t, string(spliced), "package: demo-hello\n")
	// source slice file untouched, checkout untouched
	_, err = os.Stat(filepath.Join(src, "slices", "demo-hello.yaml"))
	assert.Error(t, err, assert.AnyError)
}

func TestChoosePriorityAllExisting(t *testing.T) {
	var node yaml.Node
	src := `archives:
  a:
    priority: 30
  b:
    priority: 10
`
	assert.NoError(t, yaml.Unmarshal([]byte(src), &node))
	archives := mapValue(node.Content[0], "archives")
	p, err := choosePriority(archives)
	assert.NoError(t, err)
	assert.Equal(t, p, 9)
}

func TestChoosePriorityShiftsForPositiveSlot(t *testing.T) {
	// Lowest existing priority is 1, so there is no positive slot below it:
	// choosePriority must shift everything up and keep ours strictly positive.
	var node yaml.Node
	src := "archives:\n  a:\n    priority: 1\n  b:\n    priority: 5\n"
	assert.NoError(t, yaml.Unmarshal([]byte(src), &node))
	archives := mapValue(node.Content[0], "archives")
	p, err := choosePriority(archives)
	assert.NoError(t, err)
	assert.That(t, p >= 1, "proxy priority must be strictly positive")
	assert.Equal(t, mapValue(archives.Content[1], "priority").Value, "2")
}

func TestFormatVersion(t *testing.T) {
	dir := t.TempDir()
	write := func(s string) {
		assert.NoError(t, os.WriteFile(filepath.Join(dir, "chisel.yaml"), []byte(s), 0644))
	}
	write("format: v3\nrelease: ubuntu-24.04\n")
	n, err := FormatVersion(dir)
	assert.NoError(t, err)
	assert.Equal(t, n, 3)

	write("format: v1\n")
	n, err = FormatVersion(dir)
	assert.NoError(t, err)
	assert.Equal(t, n, 1)

	write("archives: {}\n")
	_, err = FormatVersion(dir)
	assert.Error(t, err, assert.AnyError)
}

func TestReleaseName(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "chisel.yaml"),
		[]byte("format: v3\nrelease: ubuntu-24.04\n"), 0644))
	r, err := ReleaseName(dir)
	assert.NoError(t, err)
	assert.Equal(t, r, "ubuntu-24.04")
}
