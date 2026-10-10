// Package release wires a proxy's synthetic archive into a chisel-releases
// checkout for the forwarded chisel verbs (`cut`, `info`): it copies the
// checkout to a temp dir and adds a low-priority archive entry plus the
// ephemeral public key to the copy's chisel.yaml, leaving the user's checkout
// untouched. It also renders the same blocks as text for `serve` mode to print.
package release

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lczyk/chisel-proxy/pkg/apt"
	"github.com/lczyk/chisel-proxy/pkg/deb"
	temprelease "github.com/lczyk/chisel-proxy/pkg/temp-release"
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
func Inject(src string, a *apt.Archive, version string, slicePaths []string) (dir string, cleanup func() error, err error) {
	srcYAML := filepath.Join(src, "chisel.yaml")
	raw, err := os.ReadFile(srcYAML)
	if err != nil {
		return "", nil, fmt.Errorf("cannot read %s: %w", srcYAML, err)
	}
	armor, err := a.PublicKey()
	if err != nil {
		return "", nil, err
	}
	patched, err := patchYAML(raw, a, a.KeyID(), armor, version)
	if err != nil {
		return "", nil, err
	}

	rel, err := temprelease.Copy(src)
	if err != nil {
		return "", nil, err
	}
	if err := rel.WriteChiselYAML(patched); err != nil {
		_ = rel.Remove()
		return "", nil, err
	}
	for _, sp := range slicePaths {
		if err := rel.AddSlice(sp); err != nil {
			_ = rel.Remove()
			return "", nil, err
		}
	}
	return rel.Dir, rel.Remove, nil
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
