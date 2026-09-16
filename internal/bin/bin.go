// Package bin builds a "bin" package artifact: a plain xz-compressed tarball of
// a directory tree, plus its SHA3-384 digest. This is the on-wire form chisel's
// store (bin) kind fetches and extracts, as opposed to the .deb ar archive the
// deb package uses.
package bin

import (
	"archive/tar"
	"bytes"
	"crypto/sha3"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ulikunitz/xz"
	"gopkg.in/yaml.v3"

	"github.com/lczyk/chisel-proxy/internal/deb"
)

var epoch = time.Unix(0, 0)

// Bin is one bin artifact ready to be served by the fake store.
type Bin struct {
	Name    string   // realname, e.g. "demo-hello" (the "bin-" stripped name)
	Version string   // upstream version string
	Arch    string   // Debian architecture
	Track   string   // channel track chisel will request (e.g. "1.0-ubuntu-24.04")
	Risk    string   // channel risk chisel will request (chisel hardcodes "stable")
	Files   []string // absolute install paths in the tarball
	TarXZ   []byte   // the xz-compressed tar payload
	SHA3384 string   // lowercase-hex SHA3-384 of TarXZ
	Size    int
}

// Defaults fill in metadata for a packed directory.
type Defaults struct {
	Name    string
	Version string
	Arch    string
}

// FromDir packs dir into an xz tarball. The whole tree is payload (bins have no
// DEBIAN/ metadata dir); name/version/arch come from def, defaulting to the dir
// basename, "0.0.0", and the host architecture.
func FromDir(dir string, def Defaults) (*Bin, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}
	name := def.Name
	if name == "" {
		name = filepath.Base(filepath.Clean(dir))
	}
	version := def.Version
	if version == "" {
		version = "0.0.0"
	}
	arch := def.Arch
	if arch == "" {
		if arch, err = deb.InferArch(); err != nil {
			return nil, err
		}
	}

	tarxz, files, err := buildTarXZ(dir)
	if err != nil {
		return nil, err
	}
	sum := sha3.Sum384(tarxz)
	return &Bin{
		Name:    name,
		Version: version,
		Arch:    arch,
		Files:   files,
		TarXZ:   tarxz,
		SHA3384: hex.EncodeToString(sum[:]),
		Size:    len(tarxz),
	}, nil
}

// FromTarXZ serves a prebuilt xz tarball as-is. name/version/arch come from def.
func FromTarXZ(path string, def Defaults) (*Bin, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := def.Name
	if name == "" {
		name = strings.TrimPrefix(strings.TrimSuffix(filepath.Base(path), ".tar.xz"), "bin-")
	}
	version := def.Version
	if version == "" {
		version = "0.0.0"
	}
	arch := def.Arch
	if arch == "" {
		if arch, err = deb.InferArch(); err != nil {
			return nil, err
		}
	}
	sum := sha3.Sum384(data)
	return &Bin{
		Name:    name,
		Version: version,
		Arch:    arch,
		TarXZ:   data,
		SHA3384: hex.EncodeToString(sum[:]),
		Size:    len(data),
	}, nil
}

func buildTarXZ(dir string) ([]byte, []string, error) {
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		return nil, nil, err
	}
	tw := tar.NewWriter(xw)
	var files []string

	root := filepath.Clean(dir)
	err = filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slashRel := filepath.ToSlash(rel)

		var link string
		if fi.Mode()&os.ModeSymlink != 0 {
			if link, err = os.Readlink(p); err != nil {
				return err
			}
		}
		hdr, err := tar.FileInfoHeader(fi, link)
		if err != nil {
			return err
		}
		hdr.Name = "./" + slashRel
		if fi.IsDir() {
			hdr.Name += "/"
		}
		hdr.ModTime = epoch
		hdr.Uid, hdr.Gid = 0, 0
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			defer f.Close()
			if _, err := io.Copy(tw, f); err != nil {
				return err
			}
			files = append(files, "/"+slashRel)
		} else if fi.Mode()&os.ModeSymlink != 0 {
			files = append(files, "/"+slashRel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, nil, err
	}
	if err := xw.Close(); err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	return buf.Bytes(), files, nil
}

// SDFMeta is the subset of a slice-definition file the bin path needs.
type SDFMeta struct {
	Package      string `yaml:"package"`
	DefaultTrack string `yaml:"default-track"`
}

// ReadSDFMeta parses a slice-definition file for its package name and
// default-track.
func ReadSDFMeta(path string) (SDFMeta, error) {
	var m SDFMeta
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// RealName strips the mandatory "bin-" prefix from a bin package name.
func RealName(pkg string) string { return strings.TrimPrefix(pkg, "bin-") }
