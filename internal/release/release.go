// Package release wires a proxy's synthetic archive into a chisel-releases
// checkout for `cut` mode: it copies the checkout to a temp dir and adds a
// low-priority archive entry plus the ephemeral public key to the copy's
// chisel.yaml, leaving the user's checkout untouched. It also renders the same
// blocks as text for `serve` mode to print.
package release

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lczyk/chisel-proxy/internal/apt"
	"github.com/lczyk/chisel-proxy/internal/deb"
	"github.com/lczyk/chisel-proxy/internal/sign"
)

const (
	minPriority = -1000
	maxPriority = 1000
)

// Inject copies the chisel-releases checkout at src into a fresh temp directory,
// adds the proxy archive (with the ephemeral public key inline) to that copy's
// chisel.yaml, and splices each slice-definition file in slicePaths into the
// copy's slices/ directory (by basename, overwriting). It returns the copy's
// path and a cleanup function. If version is "" it is taken from an existing
// archive entry. The user's checkout is never modified.
func Inject(src string, a *apt.Archive, signer *sign.Signer, version string, slicePaths []string) (dir string, cleanup func() error, err error) {
	srcYAML := filepath.Join(src, "chisel.yaml")
	raw, err := os.ReadFile(srcYAML)
	if err != nil {
		return "", nil, fmt.Errorf("cannot read %s: %w", srcYAML, err)
	}
	armor, err := signer.ArmoredPublicKey()
	if err != nil {
		return "", nil, err
	}
	patched, err := patchYAML(raw, a, signer.KeyID(), armor, version)
	if err != nil {
		return "", nil, err
	}

	tmp, err := os.MkdirTemp("", "chisel-proxy-release-*")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() error { return os.RemoveAll(tmp) }
	if err := copyTree(src, tmp); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, "chisel.yaml"), patched, 0644); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	slicesDir := filepath.Join(tmp, "slices")
	if err := os.MkdirAll(slicesDir, 0755); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	for _, sp := range slicePaths {
		if err := copyFile(sp, filepath.Join(slicesDir, filepath.Base(sp)), 0644); err != nil {
			_ = cleanup()
			return "", nil, fmt.Errorf("cannot splice slice %s: %w", sp, err)
		}
	}
	return tmp, cleanup, nil
}

func patchYAML(raw []byte, a *apt.Archive, keyID, armor, version string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("cannot parse chisel.yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("chisel.yaml: unexpected structure")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("chisel.yaml: top level is not a mapping")
	}

	archives := mapValue(root, "archives")
	if archives == nil {
		archives = mapValue(root, "v2-archives")
	}
	if archives == nil || archives.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("chisel.yaml: no archives section")
	}

	prio, err := choosePriority(archives)
	if err != nil {
		return nil, err
	}
	if version == "" {
		version = firstArchiveVersion(archives)
	}
	if version == "" {
		return nil, fmt.Errorf("chisel.yaml: cannot determine archive version; pass --version")
	}

	keyName := a.Suite + "-key"
	archives.Content = append(archives.Content,
		strNode(a.Suite), archiveNode(version, a.Component, a.Suite, prio, keyName))

	pubKeys := mapValue(root, "public-keys")
	if pubKeys == nil {
		pubKeys = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content, strNode("public-keys"), pubKeys)
	}
	pubKeys.Content = append(pubKeys.Content,
		strNode(keyName), pubKeyNode(keyID, armor))

	return yaml.Marshal(&doc)
}

// choosePriority assigns a priority to every archive lacking one (so upstream
// archives win) and returns a unique priority strictly below all of them for the
// proxy archive. Higher priority wins in chisel; ours must be lowest AND stay
// strictly positive, because chisel excludes negative-priority archives from
// automatic package resolution. If there is no positive slot below the existing
// minimum, all archives are shifted up to make room (safe: relative order, and
// hence collision resolution, is preserved).
func choosePriority(archives *yaml.Node) (int, error) {
	var nodes []*yaml.Node
	used := map[int]bool{}
	var missing []*yaml.Node
	for i := 0; i+1 < len(archives.Content); i += 2 {
		m := archives.Content[i+1]
		nodes = append(nodes, m)
		if pv := mapValue(m, "priority"); pv != nil {
			var v int
			if _, err := fmt.Sscanf(pv.Value, "%d", &v); err == nil {
				used[v] = true
				continue
			}
		}
		missing = append(missing, m)
	}
	next := maxPriority
	for _, m := range missing {
		for used[next] || next == 0 {
			next--
		}
		if next < minPriority {
			return 0, fmt.Errorf("cannot assign a priority to every archive")
		}
		setMapValue(m, "priority", intNode(next))
		used[next] = true
	}

	min, max := maxPriority+1, minPriority-1
	for p := range used {
		if p < min {
			min = p
		}
		if p > max {
			max = p
		}
	}
	if min > maxPriority { // no existing archives
		return maxPriority, nil
	}
	// Keep a strictly-positive slot below min. If min <= 1, shift everything up.
	if min <= 1 {
		delta := 2 - min
		if max+delta > maxPriority {
			return 0, fmt.Errorf("no room to keep the proxy archive at a positive priority")
		}
		for _, m := range nodes {
			var v int
			fmt.Sscanf(mapValue(m, "priority").Value, "%d", &v)
			setMapValue(m, "priority", intNode(v+delta))
		}
		min += delta
	}
	return min - 1, nil
}

func firstArchiveVersion(archives *yaml.Node) string {
	for i := 0; i+1 < len(archives.Content); i += 2 {
		if v := mapValue(archives.Content[i+1], "version"); v != nil {
			return v.Value
		}
	}
	return ""
}

// ReleaseName returns the top-level `release:` field of the checkout's
// chisel.yaml, which chisel uses to build the bin channel track. Empty if the
// field is absent (formats before bins).
func ReleaseName(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "chisel.yaml"))
	if err != nil {
		return "", err
	}
	var doc struct {
		Release string `yaml:"release"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return "", err
	}
	return doc.Release, nil
}

// FormatVersion returns the numeric chisel.yaml format version (e.g. 3 for
// "v3"). chisel supports bin packages only from format v3.
func FormatVersion(dir string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, "chisel.yaml"))
	if err != nil {
		return 0, err
	}
	var doc struct {
		Format string `yaml:"format"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return 0, err
	}
	if !strings.HasPrefix(doc.Format, "v") {
		return 0, fmt.Errorf("chisel.yaml: unexpected format %q", doc.Format)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(doc.Format, "v"))
	if err != nil {
		return 0, fmt.Errorf("chisel.yaml: unexpected format %q", doc.Format)
	}
	return n, nil
}

// RenderBlocks renders the archives: and public-keys: entries as pasteable YAML
// text (two-space indented, ready to drop under those top-level keys). Used by
// `serve`.
func RenderBlocks(a *apt.Archive, keyID, armor, version string, priority int) (archives, keys string) {
	keyName := a.Suite + "-key"
	var ab strings.Builder
	fmt.Fprintf(&ab, "  %s:\n", a.Suite)
	fmt.Fprintf(&ab, "    version: %s\n", version)
	fmt.Fprintf(&ab, "    components: [%s]\n", a.Component)
	fmt.Fprintf(&ab, "    suites: [%s]\n", a.Suite)
	fmt.Fprintf(&ab, "    priority: %d\n", priority)
	fmt.Fprintf(&ab, "    public-keys: [%s]\n", keyName)

	var kb strings.Builder
	fmt.Fprintf(&kb, "  %s:\n", keyName)
	fmt.Fprintf(&kb, "    id: %q\n", keyID)
	kb.WriteString("    armor: |\n")
	for _, line := range strings.Split(strings.TrimRight(armor, "\n"), "\n") {
		fmt.Fprintf(&kb, "      %s\n", line)
	}
	return ab.String(), kb.String()
}

// StubSlice renders a minimal slice-definition file pulling every path a package
// installs, pinned to the proxy archive. Printed by `serve`.
func StubSlice(suite string, p *deb.Package) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package: %s\n\n", p.Name)
	fmt.Fprintf(&b, "archive: %s\n\n", suite)
	b.WriteString("slices:\n  all:\n    contents:\n")
	for _, f := range p.Files {
		fmt.Fprintf(&b, "      %s:\n", f)
	}
	return b.String()
}

// yaml helpers

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setMapValue(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, strNode(key), val)
}

func strNode(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func intNode(n int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprintf("%d", n)}
}

func seqNode(items ...string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, it := range items {
		n.Content = append(n.Content, strNode(it))
	}
	return n
}

func archiveNode(version, component, suite string, prio int, keyName string) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content,
		strNode("version"), strNode(version),
		strNode("components"), seqNode(component),
		strNode("suites"), seqNode(suite),
		strNode("priority"), intNode(prio),
		strNode("public-keys"), seqNode(keyName),
	)
	return m
}

func pubKeyNode(keyID, armor string) *yaml.Node {
	m := &yaml.Node{Kind: yaml.MappingNode}
	m.Content = append(m.Content,
		strNode("id"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: keyID, Style: yaml.DoubleQuotedStyle},
		strNode("armor"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: armor, Style: yaml.LiteralStyle},
	)
	return m
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(os.PathSeparator)) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case fi.IsDir():
			return os.MkdirAll(target, fi.Mode().Perm())
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(p, target, fi.Mode().Perm())
		}
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
