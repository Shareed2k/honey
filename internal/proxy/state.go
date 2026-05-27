package proxy

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/safepath"
)

type stateFile struct {
	Sessions []Session `json:"sessions"`
}

// StateManager persists session data to a JSON file so multiple CLI processes
// and the Web UI can discover and cleanly terminate running proxies.
type StateManager struct {
	mu sync.Mutex
}

// NewStateManager returns a StateManager configured for the local environment.
func NewStateManager() *StateManager {
	return &StateManager{}
}

func (m *StateManager) statePath() (string, error) {
	dir, err := config.ResolveStateDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return safepath.JoinUnder(dir, "proxy_state.json")
}

func (m *StateManager) load() ([]Session, error) {
	path, err := m.statePath()
	if err != nil {
		return nil, err
	}
	// #nosec G304 -- path is strictly constructed from internal config.ResolveStateDir() and hardcoded filename
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var sf stateFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, err
	}
	return sf.Sessions, nil
}

func (m *StateManager) save(sessions []Session) error {
	path, err := m.statePath()
	if err != nil {
		return err
	}

	sf := stateFile{Sessions: sessions}
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}

	// Write to temp file then rename for atomic update
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

// List returns all active proxy sessions, removing expired ones from state.
func (m *StateManager) List() ([]Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions, err := m.load()
	if err != nil {
		return nil, err
	}

	var active []Session
	now := time.Now()
	changed := false

	for _, s := range sessions {
		if !s.ExpiresAt.IsZero() && now.After(s.ExpiresAt) {
			changed = true
			continue
		}

		if s.PID > 0 {
			process, err := os.FindProcess(s.PID)
			if err == nil {
				// Signal 0 checks if process is alive without sending a real signal
				if err := process.Signal(syscall.Signal(0)); err != nil {
					// Process is dead
					changed = true
					continue
				}
			}
		}

		active = append(active, s)
	}

	if changed {
		_ = m.save(active)
	}

	return active, nil
}

// Add adds a new session to the state.
func (m *StateManager) Add(s Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions, err := m.load()
	if err != nil && !os.IsNotExist(err) {
		// Proceed anyway if it's corrupted, we'll just overwrite it.
		sessions = []Session{}
	}

	sessions = append(sessions, s)
	return m.save(sessions)
}

// Remove removes a session from the state by ID.
func (m *StateManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions, err := m.load()
	if err != nil {
		return err
	}

	var active []Session
	found := false
	for _, s := range sessions {
		if s.ID == id {
			found = true
			continue
		}
		active = append(active, s)
	}

	if !found {
		return fmt.Errorf("session %s not found", id)
	}

	return m.save(active)
}
