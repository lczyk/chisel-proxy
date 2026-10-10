// Package temprelease builds throwaway chisel-releases directories: a copy of
// an existing checkout, or a fresh one from a chisel.yaml, with slice
// definition files spliced in. The source checkout is never modified, so a
// tool can point chisel at the copy and drop it afterwards.
package temprelease

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Release is a throwaway release directory.
type Release struct {
	Dir string
}

// Copy copies the checkout at src into a fresh temp directory, skipping .git.
// Symlinks are copied as links.
func Copy(src string) (*Release, error) {
	if _, err := os.Stat(filepath.Join(src, "chisel.yaml")); err != nil {
		return nil, fmt.Errorf("%s is not a chisel release: %w", src, err)
	}
	r, err := newDir()
	if err != nil {
		return nil, err
	}
	if err := copyTree(src, r.Dir); err != nil {
		_ = r.Remove()
		return nil, err
	}
	return r, nil
}

// New creates a fresh release directory holding chiselYAML and an empty
// slices/ directory.
func New(chiselYAML []byte) (*Release, error) {
	r, err := newDir()
	if err != nil {
		return nil, err
	}
	if err := r.WriteChiselYAML(chiselYAML); err != nil {
		_ = r.Remove()
		return nil, err
	}
	if err := os.MkdirAll(r.slicesDir(), 0755); err != nil {
		_ = r.Remove()
		return nil, err
	}
	return r, nil
}

func newDir() (*Release, error) {
	dir, err := os.MkdirTemp("", "chisel-release-*")
	if err != nil {
		return nil, err
	}
	return &Release{Dir: dir}, nil
}

// WriteChiselYAML replaces the release's chisel.yaml.
func (r *Release) WriteChiselYAML(b []byte) error {
	return os.WriteFile(filepath.Join(r.Dir, "chisel.yaml"), b, 0644)
}

// AddSlice copies the slice definition file at path into slices/, by basename,
// overwriting any existing file of that name.
func (r *Release) AddSlice(path string) error {
	if err := copyFile(path, filepath.Join(r.slicesDir(), filepath.Base(path)), 0644); err != nil {
		return fmt.Errorf("cannot splice slice %s: %w", path, err)
	}
	return nil
}

// WriteSlice writes b as slices/<pkg>.yaml, overwriting.
func (r *Release) WriteSlice(pkg string, b []byte) error {
	if err := os.MkdirAll(r.slicesDir(), 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.slicesDir(), pkg+".yaml"), b, 0644)
}

// Remove deletes the release directory.
func (r *Release) Remove() error {
	return os.RemoveAll(r.Dir)
}

func (r *Release) slicesDir() string {
	return filepath.Join(r.Dir, "slices")
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" || strings.HasPrefix(rel, ".git"+string(os.PathSeparator)) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, rel)
		switch {
		case fi.IsDir():
			return os.MkdirAll(target, fi.Mode().Perm())
		case fi.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			return copyFile(p, target, fi.Mode().Perm())
		}
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
