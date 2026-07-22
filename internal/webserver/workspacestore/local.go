// Package workspacestore persists the studio workspace layout blob on disk.
package workspacestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// workspaceFileName is the fixed, non-user-controlled name of the persisted
// workspace blob within the store directory.
const workspaceFileName = "studio_workspace.json"

// Workspace is the persisted studio layout: dockview layout JSON plus the set of
// open recipe names and the active recipe. It never contains recipe content.
type Workspace struct {
	Layout      json.RawMessage `json:"layout"`
	OpenRecipes []string        `json:"openRecipes"`
	Active      string          `json:"active"`
}

// Local persists one Workspace blob as JSON on disk. Safe for concurrent use.
type Local struct {
	mu   sync.Mutex // guards the read / write+rename critical section
	path string
}

// New returns a Local storing its blob at dir/studio_workspace.json.
func New(dir string) *Local {
	return &Local{path: filepath.Join(dir, workspaceFileName)}
}

// Load reads the stored workspace. A missing file yields the zero Workspace and
// a nil error so callers fall back to the default layout.
func (l *Local) Load(_ context.Context) (Workspace, error) {
	l.mu.Lock()
	data, err := os.ReadFile(l.path)
	l.mu.Unlock()
	if errors.Is(err, fs.ErrNotExist) {
		return Workspace{}, nil
	}
	if err != nil {
		return Workspace{}, fmt.Errorf("workspacestore: read: %w", err)
	}
	var ws Workspace
	if err := json.Unmarshal(data, &ws); err != nil {
		return Workspace{}, fmt.Errorf("workspacestore: unmarshal: %w", err)
	}
	return ws, nil
}

// Save atomically writes the workspace (temp file + rename) so concurrent Loads
// never observe a torn file.
func (l *Local) Save(_ context.Context, ws Workspace) error {
	data, err := json.Marshal(ws) // marshal outside the lock
	if err != nil {
		return fmt.Errorf("workspacestore: marshal: %w", err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return fmt.Errorf("workspacestore: mkdir: %w", err)
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("workspacestore: write temp: %w", err)
	}
	if err := os.Rename(tmp, l.path); err != nil {
		return fmt.Errorf("workspacestore: rename: %w", err)
	}
	return nil
}
