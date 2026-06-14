package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FS is a filesystem-backed Store rooted at a directory. Suitable for
// single-node deployments and tests; use the s3 backend for multi-replica.
type FS struct {
	root string
}

// NewFS creates the root directory (0700) and returns an FS store.
func NewFS(root string) (*FS, error) {
	if root == "" {
		return nil, fmt.Errorf("storage: empty root dir")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	return &FS{root: root}, nil
}

// safePath maps an opaque key to a path under root, rejecting traversal.
func (f *FS) safePath(key string) (string, error) {
	clean := filepath.Clean("/" + strings.ReplaceAll(key, "\\", "/"))
	if strings.Contains(clean, "..") {
		return "", fmt.Errorf("storage: invalid key %q", key)
	}
	return filepath.Join(f.root, filepath.FromSlash(clean)), nil
}

func (f *FS) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p, err := f.safePath(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".upload-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, p) // atomic replace
}

func (f *FS) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	p, err := f.safePath(key)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	fi, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, fi.Size(), nil
}

func (f *FS) Exists(_ context.Context, key string) (bool, error) {
	p, err := f.safePath(key)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
