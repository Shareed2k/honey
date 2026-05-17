package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
)

// LocalFileEntry mirrors RemoteFileEntry for local directories.
type LocalFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	IsDir      bool      `json:"is_dir"`
	Size       int64     `json:"size"`
	Mode       string    `json:"mode"`
	ModifiedAt time.Time `json:"modified_at"`
}

// DefaultLocalFilesRoot returns the configured or home-directory root for local file browser paths.
func DefaultLocalFilesRoot() string {
	if env := strings.TrimSpace(os.Getenv("HONEY_FILES_ROOT")); env != "" {
		return env
	}
	home, err := os.UserHomeDir()
	if err == nil && strings.TrimSpace(home) != "" {
		return home
	}
	return "."
}

// ResolveLocalPathUnderRoot resolves requested against root and ensures the result stays under root.
func ResolveLocalPathUnderRoot(root, requested string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = DefaultLocalFilesRoot()
	}
	rootAbs, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	p := strings.TrimSpace(requested)
	if p == "" {
		return rootAbs, nil
	}
	var candidate string
	if filepath.IsAbs(p) {
		candidate = filepath.Clean(p)
	} else {
		candidate = filepath.Join(rootAbs, p)
	}
	candidateAbs, err := filepath.Abs(filepath.Clean(candidate))
	if err != nil {
		return "", err
	}
	if err := safepath.Under(rootAbs, candidateAbs); err != nil {
		return "", err
	}
	return candidateAbs, nil
}

// ListLocalDirUnderRoot lists a directory under the resolved root path.
func ListLocalDirUnderRoot(root, requested string) (string, []LocalFileEntry, error) {
	resolvedPath, err := ResolveLocalPathUnderRoot(root, requested)
	if err != nil {
		return "", nil, err
	}
	entries, err := os.ReadDir(resolvedPath) // #nosec G304 -- path is root-jail validated above.
	if err != nil {
		return "", nil, err
	}
	out := make([]LocalFileEntry, 0, len(entries))
	for _, ent := range entries {
		info, err := ent.Info()
		if err != nil {
			continue
		}
		out = append(out, LocalFileEntry{
			Name:       ent.Name(),
			Path:       filepath.Join(resolvedPath, ent.Name()),
			IsDir:      ent.IsDir(),
			Size:       info.Size(),
			Mode:       info.Mode().String(),
			ModifiedAt: info.ModTime(),
		})
	}
	slices.SortFunc(out, func(a, b LocalFileEntry) int {
		if a.IsDir != b.IsDir {
			if a.IsDir {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return resolvedPath, out, nil
}

// RemoteListDir lists a directory on the remote host using a cached SSH/k8s client.
func RemoteListDir(user string, record hosts.Record, remotePath string, cache *ClientCache) ([]RemoteFileEntry, error) {
	if !hosts.IsConnectableRecord(record) {
		return nil, fmt.Errorf("record is not connectable")
	}
	client, err := cache.GetOrDial(user, record)
	if err != nil {
		return nil, err
	}
	if cache == nil {
		defer func() { _ = client.Close() }()
	}
	return client.ListRemoteDir(remotePath)
}

// RemoteCopyLocalToRemote uploads a local file to the remote path.
func RemoteCopyLocalToRemote(user string, record hosts.Record, localPath, remotePath string, cache *ClientCache) error {
	client, err := cache.GetOrDial(user, record)
	if err != nil {
		return err
	}
	if cache == nil {
		defer func() { _ = client.Close() }()
	}
	return client.Upload(localPath, remotePath)
}

// RemoteCopyRemoteToLocal downloads a remote file to a local path.
func RemoteCopyRemoteToLocal(user string, record hosts.Record, remotePath, localPath string, cache *ClientCache) error {
	client, err := cache.GetOrDial(user, record)
	if err != nil {
		return err
	}
	if cache == nil {
		defer func() { _ = client.Close() }()
	}
	return client.Download(remotePath, localPath)
}
