// Package deb builds a .deb from a directory and reads metadata back out of an
// existing one, all in-process: no dpkg, no external tooling. The ar container
// and gzip tarballs it writes are exactly what chisel's own reader consumes.
package deb

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

// Package is one .deb ready to be served: metadata, the install paths it
// carries, and the raw archive bytes.
type Package struct {
	Name    string
	Version string
	Arch    string
	Control string   // full control stanza text
	Files   []string // absolute install paths in data.tar (e.g. /usr/bin/foo)
	Data    []byte   // raw .deb bytes
	SHA256  string   // lowercase-hex digest of Data
	Size    int
}

// Defaults fill in control fields when a packed directory carries no
// DEBIAN/control of its own.
type Defaults struct {
	Name    string
	Version string
	Arch    string
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// controlField extracts a single-line control field value (case-sensitive key),
// returning "" if absent.
func controlField(control, key string) string {
	prefix := key + ":"
	for _, line := range strings.Split(control, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(line[len(prefix):])
		}
	}
	return ""
}

// decompressByName wraps r with the decompressor implied by a tar member name
// such as data.tar.gz / control.tar.xz / *.zst. The caller must Close the
// result: the zstd reader in particular holds background goroutines until then.
func decompressByName(name string, r io.Reader) (io.ReadCloser, error) {
	switch {
	case strings.HasSuffix(name, ".gz"):
		return gzip.NewReader(r)
	case strings.HasSuffix(name, ".xz"):
		zr, err := xz.NewReader(r)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(zr), nil
	case strings.HasSuffix(name, ".zst"):
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, err
		}
		return zr.IOReadCloser(), nil
	case strings.HasSuffix(name, ".tar"):
		return io.NopCloser(r), nil
	}
	return nil, fmt.Errorf("unsupported compression for %q", name)
}
