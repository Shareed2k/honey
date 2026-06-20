package webserver

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
)

// disabledPluginsCfg returns a *config.File with plugins disabled (Enabled nil → defaults to false),
// so plugins.Open is cheap and needs no .wasm files on disk.
func disabledPluginsCfg() *config.File {
	// config.Plugins.Enabled nil → WithDefaults() returns Enabled: false → NewManager returns early.
	return &config.File{}
}

func TestPluginCache_BorrowReturnsSamePointer(t *testing.T) {
	pc := newPluginCache(disabledPluginsCfg())
	defer pc.Close()

	m1, rel1 := pc.Borrow()
	defer rel1()
	require.NotNil(t, m1, "Borrow() must return non-nil manager for disabled-plugins config")

	m2, rel2 := pc.Borrow()
	defer rel2()
	require.NotNil(t, m2)
	require.Same(t, m1, m2, "Borrow() must return the same *plugins.Manager on repeated calls (single instantiation)")
}

func TestPluginCache_ReloadReturnsDifferentPointer(t *testing.T) {
	pc := newPluginCache(disabledPluginsCfg())
	defer pc.Close()

	m1, rel1 := pc.Borrow()
	rel1()
	require.NotNil(t, m1)

	cfg2 := disabledPluginsCfg()
	pc.Reload(cfg2)

	m2, rel2 := pc.Borrow()
	defer rel2()
	require.NotNil(t, m2, "Borrow() after Reload must return non-nil")
	require.NotSame(t, m1, m2, "Borrow() after Reload must return a different *plugins.Manager")

	// After reload, old manager was closed — closing again should not panic.
	require.NotPanics(t, func() { _ = m1.Close() }, "closing the old manager again must not panic")
	// Subsequent operations on the cache must not panic either.
	require.NotPanics(t, func() { pc.Close() })
}

func TestPluginCache_ConcurrentBorrow(t *testing.T) {
	const goroutines = 50

	pc := newPluginCache(disabledPluginsCfg())
	defer pc.Close()

	var wg sync.WaitGroup
	results := make([]*struct{ m interface{} }, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m, rel := pc.Borrow()
			defer rel()
			// Store a marker so the compiler cannot elide the Borrow call.
			results[idx] = &struct{ m interface{} }{m: m}
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		require.NotNil(t, r, "result slot %d is nil", i)
		require.NotNil(t, r.m, "Borrow() returned nil for goroutine %d", i)
	}
}

// TestPluginCache_RefcountDrain proves that a manager borrowed before Reload
// stays alive until its last borrow is released, and new borrows after Reload
// get the new manager.
func TestPluginCache_RefcountDrain(t *testing.T) {
	pc := newPluginCache(disabledPluginsCfg())
	defer pc.Close()

	// Borrow the initial manager but DO NOT release it yet.
	oldMgr, releaseOld := pc.Borrow()
	require.NotNil(t, oldMgr)

	// Reload with a new config — old manager must NOT be closed yet (still borrowed).
	cfg2 := disabledPluginsCfg()
	pc.Reload(cfg2)

	// A fresh Borrow must return the NEW manager.
	newMgr, releaseNew := pc.Borrow()
	defer releaseNew()
	require.NotNil(t, newMgr)
	require.NotSame(t, oldMgr, newMgr, "post-Reload Borrow must return the new manager")

	// Old manager is still alive — using it must not panic.
	require.NotPanics(t, func() { _ = oldMgr.Close() },
		"old manager must still be closeable (not double-closed) while borrow is held")

	// Releasing the old borrow must not panic even though we already called Close above.
	require.NotPanics(t, func() { releaseOld() },
		"releasing an old borrow must not panic after the manager was closed")

	// Calling Close again on the old manager must be idempotent — no panic.
	require.NotPanics(t, func() { _ = oldMgr.Close() },
		"double-close of old manager must be idempotent")
}
