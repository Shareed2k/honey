// Package safepath constrains filesystem paths for user-controlled inputs (cache roots,
// config discovery, recipe files) and performs reads/writes via os.Root where appropriate.
package safepath

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func absClean(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", fmt.Errorf("empty path")
	}
	return filepath.Abs(filepath.Clean(p))
}

// Under returns nil if path equals root or is a subdirectory of root (after absolute resolution).
func Under(root, path string) error {
	rootA, err := absClean(root)
	if err != nil {
		return err
	}
	pathA, err := absClean(path)
	if err != nil {
		return err
	}
	if rootA == pathA {
		return nil
	}
	rel, err := filepath.Rel(rootA, pathA)
	if err != nil {
		return err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q is outside %q", pathA, rootA)
	}
	return nil
}

// JoinUnder returns filepath.Join(root, parts...) if the result stays under root.
func JoinUnder(root string, parts ...string) (string, error) {
	rootA, err := absClean(root)
	if err != nil {
		return "", err
	}
	joined := filepath.Join(append([]string{rootA}, parts...)...)
	joinedA, err := absClean(joined)
	if err != nil {
		return "", err
	}
	if err := Under(rootA, joinedA); err != nil {
		return "", err
	}
	return joinedA, nil
}

// ReadFile reads path using [os.Root] on the parent directory so the basename cannot traverse.
func ReadFile(path string) (data []byte, err error) {
	abs, err := absClean(path)
	if err != nil {
		return nil, err
	}
	dir, file := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if file == "" || file == "." {
		return nil, fmt.Errorf("invalid path %q", abs)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := r.Close(); cerr != nil && err == nil {
			err = cerr
			data = nil
		}
	}()
	return r.ReadFile(file)
}

// Stat stats path using [os.Root] on the parent directory.
func Stat(path string) (info os.FileInfo, err error) {
	abs, err := absClean(path)
	if err != nil {
		return nil, err
	}
	dir, file := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if file == "" || file == "." {
		return nil, fmt.Errorf("invalid path %q", abs)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		// Fallback for systems where os.OpenRoot is restricted (e.g. Android)
		return os.Stat(filepath.Clean(abs))
	}
	defer func() {
		if cerr := r.Close(); cerr != nil && err == nil {
			err = cerr
			info = nil
		}
	}()
	return r.Stat(file)
}

// OpenReadWriteProbe returns nil if path is a regular file that can be opened read-write via [os.Root]
// on the parent directory (used to detect user-writable known_hosts files).
func OpenReadWriteProbe(path string) (err error) {
	abs, err := absClean(path)
	if err != nil {
		return err
	}
	dir, file := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if file == "" || file == "." {
		return fmt.Errorf("invalid path %q", abs)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		// Fallback for systems where os.OpenRoot is restricted (e.g. Android)
		f, err := os.OpenFile(filepath.Clean(abs), os.O_RDWR, 0)
		if err != nil {
			return err
		}
		return f.Close()
	}
	defer func() {
		if cerr := r.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	f, err := r.OpenFile(file, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	if cerr := f.Close(); cerr != nil {
		return cerr
	}
	return nil
}

// WriteFile writes data to path using a temp file and rename within [os.Root] of the parent directory.
func WriteFile(path string, data []byte, perm os.FileMode) (err error) {
	abs, err := absClean(path)
	if err != nil {
		return err
	}
	dir, file := filepath.Split(abs)
	dir = filepath.Clean(dir)
	if file == "" || file == "." {
		return fmt.Errorf("invalid path %q", abs)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		// Fallback for systems where os.OpenRoot is restricted (e.g. Android)
		tmpAbs := filepath.Join(dir, file+".tmp")
		if werr := os.WriteFile(tmpAbs, data, perm); werr != nil {
			return werr
		}
		return os.Rename(tmpAbs, filepath.Clean(abs))
	}
	defer func() {
		if cerr := r.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	tmp := file + ".tmp"
	if err := r.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return r.Rename(tmp, file)
}
