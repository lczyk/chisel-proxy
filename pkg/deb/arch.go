package deb

import (
	"fmt"
	"runtime"
)

// archMap mirrors chisel's own runtime-to-Debian architecture table so a
// synthesised package labels itself the way chisel expects, without shelling
// out to dpkg.
var archMap = map[string]string{
	"386":     "i386",
	"amd64":   "amd64",
	"arm":     "armhf",
	"arm64":   "arm64",
	"ppc64le": "ppc64el",
	"riscv64": "riscv64",
	"s390x":   "s390x",
}

// arches is every Debian architecture chisel accepts, in the order ubuntu
// lists them.
var arches = []string{"amd64", "arm64", "armhf", "i386", "ppc64el", "riscv64", "s390x"}

// cpus is the names an architecture's cpu goes by in paths: the cpu part of
// the multiarch triplet (x86_64-linux-gnu) and, for i386, the i686 that
// some packages use instead.
var cpus = map[string][]string{
	"amd64":   {"x86_64"},
	"arm64":   {"aarch64"},
	"armhf":   {"arm"},
	"i386":    {"i386", "i686"},
	"ppc64el": {"powerpc64le"},
	"riscv64": {"riscv64"},
	"s390x":   {"s390x"},
}

// Arches lists every Debian architecture chisel accepts, in the order ubuntu
// lists them. The slice is the caller's to keep.
func Arches() []string {
	return append([]string(nil), arches...)
}

// CPUs lists the names arch's cpu goes by in paths, the multiarch triplet's
// first part among them, or nil for an architecture chisel does not accept.
func CPUs(arch string) []string {
	return append([]string(nil), cpus[arch]...)
}

// InferArch returns the Debian architecture for the running platform.
func InferArch() (string, error) {
	if a, ok := archMap[runtime.GOARCH]; ok {
		return a, nil
	}
	return "", fmt.Errorf("cannot infer package architecture from %q", runtime.GOARCH)
}

// ValidArch reports whether arch is a Debian architecture chisel accepts.
func ValidArch(arch string) bool {
	_, ok := cpus[arch]
	return ok
}
