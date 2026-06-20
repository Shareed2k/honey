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

func TestPluginCache_GetReturnsSamePointer(t *testing.T) {
	pc := newPluginCache(disabledPluginsCfg())
	defer pc.Close()

	m1 := pc.Get()
	require.NotNil(t, m1, "Get() must return non-nil manager for disabled-plugins config")

	m2 := pc.Get()
	require.NotNil(t, m2)
	require.Same(t, m1, m2, "Get() must return the same *plugins.Manager on repeated calls (single instantiation)")
}

func TestPluginCache_ReloadReturnsDifferentPointer(t *testing.T) {
	pc := newPluginCache(disabledPluginsCfg())
	defer pc.Close()

	m1 := pc.Get()
	require.NotNil(t, m1)

	cfg2 := disabledPluginsCfg()
	pc.Reload(cfg2)

	m2 := pc.Get()
	require.NotNil(t, m2, "Get() after Reload must return non-nil")
	require.NotSame(t, m1, m2, "Get() after Reload must return a different *plugins.Manager")

	// After reload, old manager was closed — closing again should not panic.
	require.NotPanics(t, func() { _ = m1.Close() }, "closing the old manager again must not panic")
	// Subsequent operations on the cache must not panic either.
	require.NotPanics(t, func() { pc.Close() })
}

func TestPluginCache_ConcurrentGet(t *testing.T) {
	const goroutines = 50

	pc := newPluginCache(disabledPluginsCfg())
	defer pc.Close()

	var wg sync.WaitGroup
	results := make([]*struct{ m interface{} }, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m := pc.Get()
			// Store a marker so the compiler cannot elide the Get call.
			results[idx] = &struct{ m interface{} }{m: m}
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		require.NotNil(t, r, "result slot %d is nil", i)
		require.NotNil(t, r.m, "Get() returned nil for goroutine %d", i)
	}
}
