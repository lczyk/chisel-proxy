package deb

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/blakesmith/ar"
)

// FromFile reads an existing .deb: its control metadata and install paths, plus
// the raw bytes for serving.
func FromFile(p string) (*Package, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	control, err := readControl(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	name := controlField(control, "Package")
	version := controlField(control, "Version")
	arch := controlField(control, "Architecture")
	if name == "" || version == "" || arch == "" {
		return nil, fmt.Errorf("%s: control missing Package/Version/Architecture", p)
	}
	files, err := readDataFiles(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &Package{
		Name:    name,
		Version: version,
		Arch:    arch,
		Control: control,
		Files:   files,
		Data:    data,
		SHA256:  sha256hex(data),
		Size:    len(data),
	}, nil
}

func readControl(deb []byte) (string, error) {
	r := ar.NewReader(bytes.NewReader(deb))
	for {
		hdr, err := r.Next()
		if err == io.EOF {
			return "", fmt.Errorf("no control.tar member")
		}
		if err != nil {
			return "", err
		}
		if !strings.HasPrefix(hdr.Name, "control.tar") {
			continue
		}
		dr, err := decompressByName(hdr.Name, r)
		if err != nil {
			return "", err
		}
		defer dr.Close()
		tr := tar.NewReader(dr)
		for {
			th, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if path.Clean(strings.TrimPrefix(th.Name, "./")) == "control" {
				b, err := io.ReadAll(tr)
				if err != nil {
					return "", err
				}
				s := string(b)
				if !strings.HasSuffix(s, "\n") {
					s += "\n"
				}
				return s, nil
			}
		}
		return "", fmt.Errorf("control file not found in control.tar")
	}
}

func readDataFiles(deb []byte) ([]string, error) {
	r := ar.NewReader(bytes.NewReader(deb))
	for {
		hdr, err := r.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("no data.tar member")
		}
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(hdr.Name, "data.tar") {
			continue
		}
		dr, err := decompressByName(hdr.Name, r)
		if err != nil {
			return nil, err
		}
		defer dr.Close()
		tr := tar.NewReader(dr)
		var files []string
		for {
			th, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if th.Typeflag == tar.TypeReg || th.Typeflag == tar.TypeSymlink {
				clean := path.Clean(strings.TrimPrefix(th.Name, "./"))
				files = append(files, "/"+strings.TrimPrefix(clean, "/"))
			}
		}
		sort.Strings(files)
		return files, nil
	}
}
