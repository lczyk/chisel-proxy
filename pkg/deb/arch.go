package deb

import (
	"fmt"
	"runtime"
)

// archMap mirrors chisel's own runtime-to-Debian architecture table so a
// synthesised package labels itself the way chisel expects, without shelling
// out to dpkg. chisel keeps its table internal, so the tables of this file
// repeat it on purpose, and are the one copy other packages should take.
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

// words is what names an architecture as a word of a path: its own name,
// the cpu of its multiarch triplet, and what debian's cross toolchain
// package names, java, go, dotnet, libunwind, ruby and the kernel's arch
// directories call it. x86 is both amd64's and i386's, as in the kernel
// headers' arch/x86.
var words = map[string][]string{
	"amd64":   {"amd64", "x86_64", "x86-64", "x64", "x86"},
	"arm64":   {"arm64", "aarch64"},
	"armhf":   {"armhf", "arm"},
	"i386":    {"i386", "i686", "x86", "386"},
	"ppc64el": {"ppc64el", "powerpc64le", "ppc64le", "ppc64"},
	"riscv64": {"riscv64", "riscv"},
	"s390x":   {"s390x", "s390"},
}

// Arches lists every Debian architecture chisel accepts, in the order ubuntu
// lists them. The slice is the caller's to keep.
func Arches() []string {
	return append([]string(nil), arches...)
}

// Words lists what names arch as a word of a path, its own name first, or
// nil for an architecture chisel does not accept. Two architectures may
// share a word. The slice is the caller's to keep.
func Words(arch string) []string {
	return append([]string(nil), words[arch]...)
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
	_, ok := words[arch]
	return ok
}
