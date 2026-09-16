package cli

import (
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// minGoMinorDarwin is the first Go minor whose crypto/x509 honours
// SSL_CERT_FILE on darwin (behind the x509sslcertoverrideplatform GODEBUG).
const minGoMinorDarwin = 27

// checkBinPlatform verifies the bin (TLS-interception) path can work here. The
// child trusts our CA via SSL_CERT_FILE: always honoured on Linux, and on macOS
// only by a chisel built with Go 1.27+, so the binary's build info is checked.
func checkBinPlatform(chiselBin string) error {
	switch runtime.GOOS {
	case "linux":
		return nil
	case "darwin":
		path, err := exec.LookPath(chiselBin)
		if err != nil {
			return fmt.Errorf("cannot find chisel binary %q: %w", chiselBin, err)
		}
		bi, err := buildinfo.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read Go build info from %s: %w", path, err)
		}
		major, minor, ok := parseGoVersion(bi.GoVersion)
		if !ok {
			return fmt.Errorf("cannot determine the Go version %s was built with (%q)", path, bi.GoVersion)
		}
		if major < 1 || (major == 1 && minor < minGoMinorDarwin) {
			return fmt.Errorf("bin injection on macOS needs a chisel built with Go 1.%d+ (its crypto/x509 must honour SSL_CERT_FILE); %s was built with %s, rebuild it with a newer toolchain",
				minGoMinorDarwin, path, bi.GoVersion)
		}
		return nil
	default:
		return fmt.Errorf("bin injection is not supported on %s", runtime.GOOS)
	}
}

// parseGoVersion extracts major.minor from a buildinfo GoVersion such as
// "go1.27.1", "go1.27rc1" or "devel go1.28-abcdef".
func parseGoVersion(v string) (major, minor int, ok bool) {
	i := strings.Index(v, "go")
	if i < 0 {
		return 0, 0, false
	}
	parts := strings.SplitN(v[i+2:], ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	if major, ok = leadingInt(parts[0]); !ok {
		return 0, 0, false
	}
	if minor, ok = leadingInt(parts[1]); !ok {
		return 0, 0, false
	}
	return major, minor, true
}

func leadingInt(s string) (int, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:i])
	return n, err == nil
}

// mergeGODEBUG appends setting to an existing GODEBUG value; later entries win.
func mergeGODEBUG(existing, setting string) string {
	if existing == "" {
		return setting
	}
	return existing + "," + setting
}

// systemRootsPEM returns the platform CA bundle as PEM, or nil if none is found.
// SSL_CERT_FILE replaces the platform roots entirely, so the bundle handed to
// chisel must carry them for any other HTTPS it does.
func systemRootsPEM() []byte {
	if runtime.GOOS == "darwin" {
		out, err := exec.Command("security", "find-certificate", "-a", "-p",
			"/System/Library/Keychains/SystemRootCertificates.keychain").Output()
		if err == nil {
			return out
		}
		return nil
	}
	for _, p := range []string{"/etc/ssl/certs/ca-certificates.crt", "/etc/pki/tls/certs/ca-bundle.crt"} {
		if b, err := os.ReadFile(p); err == nil {
			return b
		}
	}
	return nil
}
