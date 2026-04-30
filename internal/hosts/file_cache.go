package hosts

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CacheEntry is a single cached provider result.
type CacheEntry struct {
	StoredAt time.Time `json:"stored_at"`
	Records  []Record   `json:"records"`
}

// FileCache is a simple JSON file cache with TTL.
type FileCache struct {
	path string
	mu   sync.Mutex
	ttl  time.Duration
}

// NewFileCache creates a JSON-backed cache at path with ttl.
func NewFileCache(path string, ttl time.Duration) *FileCache {
	return &FileCache{path: path, ttl: ttl}
}

// DefaultCacheDir returns XDG-style cache directory for honey.
func DefaultCacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "honey")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

type diskDoc struct {
	Entries map[string]CacheEntry `json:"entries"`
}

func (c *FileCache) load() (diskDoc, error) {
	var doc diskDoc
	doc.Entries = make(map[string]CacheEntry)
	if c.path == "" {
		return doc, errors.New("cache path empty")
	}
	b, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return doc, nil
		}
		return doc, err
	}
	if len(b) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return doc, err
	}
	if doc.Entries == nil {
		doc.Entries = make(map[string]CacheEntry)
	}
	return doc, nil
}

func (c *FileCache) save(doc diskDoc) error {
	if c.path == "" {
		return errors.New("cache path empty")
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := c.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.path)
}

// CacheKeySHA256 builds a stable cache key from provider id and payload.
func CacheKeySHA256(providerID string, payload []byte) string {
	h := sha256.New()
	h.Write([]byte(providerID))
	h.Write([]byte{0})
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}

// Get returns records and true if fresh.
func (c *FileCache) Get(cacheKey string) ([]Record, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	doc, err := c.load()
	if err != nil {
		return nil, false, err
	}
	e, ok := doc.Entries[cacheKey]
	if !ok {
		return nil, false, nil
	}
	if c.ttl > 0 && time.Since(e.StoredAt) > c.ttl {
		return nil, false, nil
	}
	return e.Records, true, nil
}

// Set stores records for key.
func (c *FileCache) Set(cacheKey string, records []Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	doc, err := c.load()
	if err != nil {
		return err
	}
	doc.Entries[cacheKey] = CacheEntry{StoredAt: time.Now(), Records: records}
	return c.save(doc)
}
