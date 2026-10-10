//go:build e2e

// Package e2e drives a real `chisel cut` through the proxy: it packs a demo
// package, injects it into a real chisel-releases clone, cuts a rootfs, and
// checks the package landed. It also runs `chisel info` on a spliced slice. Gated behind the e2e build tag and CHISEL_PROXY_E2E
// because it needs the chisel binary and network access to the real archive.
package e2e

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func requireE2E(t *testing.T) {
	t.Helper()
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
}

// cloneRelease shallow-clones a real chisel-releases checkout under dir.
func cloneRelease(t *testing.T, dir string) string {
	t.Helper()
	release := filepath.Join(dir, "releases")
	clone := exec.Command("git", "clone", "--quiet", "--depth", "1", "-b", releaseBranch,
		"https://github.com/canonical/chisel-releases", release)
	clone.Stdout, clone.Stderr = os.Stderr, os.Stderr
	if err := clone.Run(); err != nil {
		t.Fatalf("clone chisel-releases: %v", err)
	}
	return release
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns what
// it wrote. cli.Main hands os.Stdout straight to the chisel subprocess.
func captureStdout(t *testing.T, fn func() int) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	code := fn()
	os.Stdout = orig
	_ = w.Close()
	return code, <-done
}

func TestInfoShowsSplicedSlice(t *testing.T) {
	requireE2E(t)
	tmp := t.TempDir()
	release := cloneRelease(t, tmp)

	// The slice lives outside the checkout; info must see it only via splicing.
	slice := filepath.Join(tmp, "demo-hello.yaml")
	mustWrite(t, slice,
		"package: demo-hello\n\nslices:\n  bins:\n    contents:\n      /usr/bin/demo-hello:\n", 0644)

	code, out := captureStdout(t, func() int {
		return cli.Main([]string{"info", "--release", release, slice, "--", "demo-hello_bins"})
	})
	if code != 0 {
		t.Fatalf("chisel-proxy info exited %d", code)
	}
	for _, want := range []string{"package: demo-hello", "bins:", "/usr/bin/demo-hello"} {
		if !strings.Contains(out, want) {
			t.Fatalf("chisel info output missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(filepath.Join(release, "slices", "demo-hello.yaml")); err == nil {
		t.Fatalf("info modified the release checkout")
	}
}

func TestCutInjectsPackageThroughProxy(t *testing.T) {
	requireE2E(t)
	tmp := t.TempDir()

	// A demo package as a loose directory; chisel-proxy packs it. Its name comes
	// from the directory basename.
	const script = "#!/bin/sh\nprintf 'hello from chisel-proxy\\n'\n"
	pkgDir := filepath.Join(tmp, "demo-hello")
	mustWrite(t, filepath.Join(pkgDir, "usr", "bin", "demo-hello"), script, 0755)

	release := cloneRelease(t, tmp)

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
