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

// InferArch returns the Debian architecture for the running platform.
func InferArch() (string, error) {
	if a, ok := archMap[runtime.GOARCH]; ok {
		return a, nil
	}
	return "", fmt.Errorf("cannot infer package architecture from %q", runtime.GOARCH)
}

// ValidArch reports whether arch is a Debian architecture chisel accepts.
func ValidArch(arch string) bool {
	for _, a := range archMap {
		if a == arch {
			return true
		}
	}
	return false
}
