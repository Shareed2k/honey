package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultMaxConns = 4

// PoolManager caches pgx pools keyed by DSN hash.
type PoolManager struct {
	mu    sync.Mutex
	pools map[string]*pgxpool.Pool
}

// NewPoolManager creates an empty pool manager.
func NewPoolManager() *PoolManager {
	return &PoolManager{pools: make(map[string]*pgxpool.Pool)}
}

// Acquire returns a pooled connection for dsn.
func (m *PoolManager) Acquire(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if m == nil {
		return nil, fmt.Errorf("postgres: pool manager is nil")
	}
	key := dsnKey(dsn)
	m.mu.Lock()
	if p, ok := m.pools[key]; ok {
		m.mu.Unlock()
		return p, nil
	}
	m.mu.Unlock()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	if cfg.MaxConns == 0 {
		cfg.MaxConns = defaultMaxConns
	}
	cfg.MaxConnIdleTime = 5 * time.Minute
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.pools[key]; ok {
		pool.Close()
		return existing, nil
	}
	m.pools[key] = pool
	return pool, nil
}

// Close closes all cached pools.
func (m *PoolManager) Close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.pools {
		p.Close()
	}
	m.pools = make(map[string]*pgxpool.Pool)
}

func dsnKey(dsn string) string {
	sum := sha256.Sum256([]byte(dsn))
	return hex.EncodeToString(sum[:])
}
