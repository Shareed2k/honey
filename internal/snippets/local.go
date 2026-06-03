package snippets

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sync"

	"github.com/shareed2k/honey/internal/safepath"
)

// LocalStore persists snippets as a JSON array in a single file on disk.
type LocalStore struct {
	mu   sync.Mutex
	path string
}

// NewLocalStore returns a file-backed Store at path.
func NewLocalStore(path string) *LocalStore {
	return &LocalStore{path: path}
}

// loadFile reads the snippet list; a missing file yields an empty list.
func (s *LocalStore) loadFile() ([]ExecSnippet, error) {
	b, err := safepath.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []ExecSnippet{}, nil
		}
		return nil, fmt.Errorf("read snippets: %w", err)
	}
	if len(b) == 0 {
		return []ExecSnippet{}, nil
	}
	var list []ExecSnippet
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("parse snippets: %w", err)
	}
	if list == nil {
		list = []ExecSnippet{}
	}
	return list, nil
}

func (s *LocalStore) writeFile(list []ExecSnippet) error {
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snippets: %w", err)
	}
	if err := safepath.WriteFile(s.path, b, 0o600); err != nil {
		return fmt.Errorf("write snippets: %w", err)
	}
	return nil
}

// List returns all stored snippets.
func (s *LocalStore) List(_ context.Context) ([]ExecSnippet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadFile()
}

// Save upserts by ID, generating an ID when empty, and returns the stored snippet.
func (s *LocalStore) Save(_ context.Context, snip ExecSnippet) (ExecSnippet, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadFile()
	if err != nil {
		return ExecSnippet{}, err
	}
	if snip.ID == "" {
		id, err := newID()
		if err != nil {
			return ExecSnippet{}, err
		}
		snip.ID = id
	}
	replaced := false
	for i := range list {
		if list[i].ID == snip.ID {
			list[i] = snip
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, snip)
	}
	if err := s.writeFile(list); err != nil {
		return ExecSnippet{}, err
	}
	return snip, nil
}

// Delete removes a snippet by ID, returning ErrNotFound if absent.
func (s *LocalStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.loadFile()
	if err != nil {
		return err
	}
	out := make([]ExecSnippet, 0, len(list))
	found := false
	for _, snip := range list {
		if snip.ID == id {
			found = true
			continue
		}
		out = append(out, snip)
	}
	if !found {
		return ErrNotFound
	}
	return s.writeFile(out)
}

func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate snippet id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
