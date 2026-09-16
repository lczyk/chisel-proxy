package deb

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blakesmith/ar"
)

// epoch is a fixed timestamp for archive members, keeping builds reproducible.
var epoch = time.Unix(0, 0)

// FromDir packs dir into a .deb. If dir/DEBIAN/control exists it is used
// verbatim; otherwise a minimal control stanza is fabricated from def. Files
// under DEBIAN/ are package metadata, everything else is payload.
func FromDir(dir string, def Defaults) (*Package, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", dir)
	}

	control, err := controlForDir(dir)
	if err != nil {
		return nil, err
	}

	name := controlField(control, "Package")
	if name == "" {
		name = def.Name
	}
	if name == "" {
		name = filepath.Base(filepath.Clean(dir))
	}
	version := controlField(control, "Version")
	if version == "" {
		version = def.Version
	}
	if version == "" {
		version = "0.0.0"
	}
	arch := controlField(control, "Architecture")
	if arch == "" {
		arch = def.Arch
	}
	if arch == "" {
		if arch, err = InferArch(); err != nil {
			return nil, err
		}
	}

	if control == "" {
		control = fabricateControl(name, version, arch)
	}

	dataTar, files, err := buildDataTar(dir)
	if err != nil {
		return nil, err
	}
	controlTar, err := buildControlTar(control)
	if err != nil {
		return nil, err
	}
	debBytes, err := buildAr(controlTar, dataTar)
	if err != nil {
		return nil, err
	}

	return &Package{
		Name:    name,
		Version: version,
		Arch:    arch,
		Control: control,
		Files:   files,
		Data:    debBytes,
		SHA256:  sha256hex(debBytes),
		Size:    len(debBytes),
	}, nil
}

// controlForDir returns the verbatim DEBIAN/control text if present, else "".
func controlForDir(dir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "DEBIAN", "control"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	control := string(b)
	if !strings.HasSuffix(control, "\n") {
		control += "\n"
	}
	return control, nil
}

func fabricateControl(name, version, arch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Package: %s\n", name)
	fmt.Fprintf(&b, "Version: %s\n", version)
	fmt.Fprintf(&b, "Architecture: %s\n", arch)
	b.WriteString("Maintainer: chisel-proxy <chisel-proxy@localhost>\n")
	b.WriteString("Description: Package injected by chisel-proxy.\n")
	return b.String()
}

// buildDataTar tars dir (excluding the top-level DEBIAN/ subtree) into a gzipped
// data.tar and returns the compressed bytes plus the sorted list of install
// paths (regular files and symlinks).
func buildDataTar(dir string) ([]byte, []string, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var files []string

	root := filepath.Clean(dir)
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
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
		if slashRel == "DEBIAN" || strings.HasPrefix(slashRel, "DEBIAN/") {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

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
	if err := gz.Close(); err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	return buf.Bytes(), files, nil
}

func buildControlTar(control string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(control)
	hdr := &tar.Header{
		Name:     "./control",
		Mode:     0644,
		Size:     int64(len(body)),
		ModTime:  epoch,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return nil, err
	}
	if _, err := tw.Write(body); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func buildAr(controlTarGz, dataTarGz []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := ar.NewWriter(&buf)
	if err := w.WriteGlobalHeader(); err != nil {
		return nil, err
	}
	members := []struct {
		name string
		body []byte
	}{
		{"debian-binary", []byte("2.0\n")},
		{"control.tar.gz", controlTarGz},
		{"data.tar.gz", dataTarGz},
	}
	for _, m := range members {
		hdr := &ar.Header{
			Name:    m.name,
			ModTime: epoch,
			Mode:    0644,
			Size:    int64(len(m.body)),
		}
		if err := w.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := w.Write(m.body); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}
