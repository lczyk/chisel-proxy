//go:build e2e

// Package e2e drives a real `chisel cut` through the proxy: it packs a demo
// package, injects it into a real chisel-releases clone, cuts a rootfs, and
// checks the package landed. Gated behind the e2e build tag and CHISEL_PROXY_E2E
// because it needs the chisel binary and network access to the real archive.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lczyk/chisel-proxy/internal/cli"
)

const releaseBranch = "ubuntu-24.04"

func mustWrite(t *testing.T, p, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestCutInjectsPackageThroughProxy(t *testing.T) {
	if os.Getenv("CHISEL_PROXY_E2E") != "1" {
		t.Skip("set CHISEL_PROXY_E2E=1 to run (needs chisel + network)")
	}
	chisel := os.Getenv("CHISEL")
	if chisel == "" {
		chisel = "chisel"
	}
	if _, err := exec.LookPath(chisel); err != nil {
		t.Skipf("chisel binary not found (%v); set CHISEL=/path/to/chisel", err)
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}

	tmp := t.TempDir()

	// A demo package as a loose directory; chisel-proxy packs it. Its name comes
	// from the directory basename.
	const script = "#!/bin/sh\nprintf 'hello from chisel-proxy\\n'\n"
	pkgDir := filepath.Join(tmp, "demo-hello")
	mustWrite(t, filepath.Join(pkgDir, "usr", "bin", "demo-hello"), script, 0755)

	// A real chisel-releases checkout.
	release := filepath.Join(tmp, "releases")
	clone := exec.Command("git", "clone", "--quiet", "--depth", "1", "-b", releaseBranch,
		"https://github.com/canonical/chisel-releases", release)
	clone.Stdout, clone.Stderr = os.Stderr, os.Stderr
	if err := clone.Run(); err != nil {
		t.Fatalf("clone chisel-releases: %v", err)
	}

	// Author a slice for the package. No `archive:` pin: this exercises chisel's
	// priority fallback to the injected low-priority archive.
	mustWrite(t, filepath.Join(release, "slices", "demo-hello.yaml"),
		"package: demo-hello\n\nslices:\n  bins:\n    contents:\n      /usr/bin/demo-hello:\n", 0644)

	// chisel requires --root to exist (as demo-small.sh's `mkdir -p rootfs`).
	rootfs := filepath.Join(tmp, "rootfs")
	if err := os.MkdirAll(rootfs, 0755); err != nil {
		t.Fatal(err)
	}

	// demo-hello_bins comes from our archive; base-files_base proves passthrough
	// to the real upstream archive still works.
	code := cli.Main([]string{
		"cut", "--release", release, pkgDir, "--",
		"demo-hello_bins", "base-files_base", "--root", rootfs,
	})
	if code != 0 {
		t.Fatalf("chisel-proxy cut exited %d", code)
	}

	got, err := os.ReadFile(filepath.Join(rootfs, "usr", "bin", "demo-hello"))
	if err != nil {
		t.Fatalf("injected file missing from rootfs: %v", err)
	}
	if string(got) != script {
		t.Fatalf("injected file content mismatch: %q", got)
	}
	// base-files should have landed too (passthrough worked).
	if _, err := os.Stat(filepath.Join(rootfs, "etc")); err != nil {
		t.Fatalf("base-files_base did not land (passthrough failed?): %v", err)
	}
}
