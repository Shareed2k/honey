package hosts

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFileCacheTTL(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cache.json")
	c := NewFileCache(p, 50*time.Millisecond)
	key := CacheKeySHA256("aws", []byte("test"))
	rec := []Record{{Provider: "aws", Name: "i", PrimaryIP: "1.2.3.4"}}
	if err := c.Set(key, rec); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Get(key)
	if err != nil || !ok || len(got) != 1 {
		t.Fatalf("get fresh: ok=%v err=%v len=%d", ok, err, len(got))
	}
	time.Sleep(60 * time.Millisecond)
	_, ok2, err := c.Get(key)
	if err != nil || ok2 {
		t.Fatalf("expected stale cache, ok=%v err=%v", ok2, err)
	}
}

func TestCacheKeySHA256Stable(t *testing.T) {
	a := CacheKeySHA256("gcp", []byte("p"))
	b := CacheKeySHA256("gcp", []byte("p"))
	if a != b {
		t.Fatal("cache key not stable")
	}
}
