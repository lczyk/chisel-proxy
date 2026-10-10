package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lczyk/assert"
	"gopkg.in/yaml.v3"
)

// fakeChisel records its argv, proxy env, the release tree it was pointed at
// and that tree's chisel.yaml under out/<verb>/, then exits 3.
const fakeChisel = `#!/bin/sh
d="$FAKE_CHISEL_OUT/$1"
mkdir -p "$d"
printf '%s\n' "$@" > "$d/argv"
printf '%s\n' "$http_proxy" > "$d/proxy"
(cd "$3" && find . -type f | sort) > "$d/tree"
cp "$3/chisel.yaml" "$d/chisel.yaml"
exit 3
`

type seen struct {
	argv     []string
	proxy    string
	tree     string
	archives map[string]struct {
		Priority int `yaml:"priority"`
	}
	keys []string
}

func readSeen(t *testing.T, dir string) seen {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		assert.NoError(t, err)
		return string(b)
	}
	var s seen
	s.argv = strings.Split(strings.TrimSpace(read("argv")), "\n")
	s.proxy = strings.TrimSpace(read("proxy"))
	s.tree = read("tree")
	var doc struct {
		Archives map[string]struct {
			Priority int `yaml:"priority"`
		} `yaml:"archives"`
		PublicKeys map[string]any `yaml:"public-keys"`
	}
	assert.NoError(t, yaml.Unmarshal([]byte(read("chisel.yaml")), &doc))
	s.archives = doc.Archives
	for k := range doc.PublicKeys {
		s.keys = append(s.keys, k)
	}
	return s
}

func TestChiselVerbsSeeSameSetup(t *testing.T) {
	tmp := t.TempDir()
	rel := filepath.Join(tmp, "release")
	assert.NoError(t, os.MkdirAll(filepath.Join(rel, "slices"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(rel, "chisel.yaml"), []byte(
		"format: v2\n\narchives:\n  ubuntu:\n    version: 24.04\n    components: [main]\n    suites: [noble]\n    priority: 20\n    public-keys: [ubuntu-key]\n\npublic-keys:\n  ubuntu-key:\n    id: \"0000000000000000\"\n    armor: |\n      placeholder\n"), 0644))
	pkgDir := filepath.Join(tmp, "demo")
	assert.NoError(t, os.MkdirAll(filepath.Join(pkgDir, "usr", "bin"), 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(pkgDir, "usr", "bin", "demo"), []byte("x"), 0755))
	slice := filepath.Join(tmp, "demo.yaml")
	assert.NoError(t, os.WriteFile(slice, []byte("package: demo\n"), 0644))

	script := filepath.Join(tmp, "chisel")
	assert.NoError(t, os.WriteFile(script, []byte(fakeChisel), 0755))
	out := filepath.Join(tmp, "out")
	t.Setenv("CHISEL", script)
	t.Setenv("FAKE_CHISEL_OUT", out)

	for _, verb := range []string{"cut", "info"} {
		code := Main([]string{verb, "--release", rel, "--arch", "amd64", pkgDir, slice, "--", "demo_bins"})
		assert.Equal(t, code, 3)
	}

	cut := readSeen(t, filepath.Join(out, "cut"))
	info := readSeen(t, filepath.Join(out, "info"))

	assert.Equal(t, cut.argv[0], "cut")
	assert.Equal(t, info.argv[0], "info")
	assert.EqualArrays(t, cut.argv[1:2], []string{"--release"})
	assert.EqualArrays(t, info.argv[1:2], []string{"--release"})
	assert.EqualArrays(t, cut.argv[3:], info.argv[3:])

	assert.ContainsString(t, cut.proxy, "http://127.0.0.1:")
	assert.ContainsString(t, info.proxy, "http://127.0.0.1:")
	assert.Equal(t, cut.tree, info.tree)
	assert.ContainsString(t, info.tree, "./slices/demo.yaml")

	assert.Len(t, info.archives, 2)
	assert.Equal(t, cut.archives["chisel-proxy"].Priority, info.archives["chisel-proxy"].Priority)
	assert.Equal(t, cut.archives["ubuntu"].Priority, info.archives["ubuntu"].Priority)
	assert.Len(t, info.keys, 2)
}

func TestChiselVerbArgumentErrors(t *testing.T) {
	for _, verb := range []string{"cut", "info"} {
		t.Run(verb+" no forwarded args", func(t *testing.T) {
			assert.Equal(t, Main([]string{verb, "x.yaml"}), 2)
		})
		t.Run(verb+" release forwarded", func(t *testing.T) {
			assert.Equal(t, Main([]string{verb, "x.yaml", "--", "a_b", "--release=/r"}), 2)
		})
	}
}
