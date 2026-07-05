package plugincache_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	plugincache "github.com/shareed2k/honey/internal/plugincache"
)

func disabledPluginsCfg() *config.File { return &config.File{} }

func TestCache_BorrowReturnsSamePointer(t *testing.T) {
	t.Parallel()
	pc := plugincache.New(disabledPluginsCfg())
	defer pc.Close()

	m1, rel1 := pc.Borrow()
	defer rel1()
	require.NotNil(t, m1, "Borrow must return non-nil manager")

	m2, rel2 := pc.Borrow()
	defer rel2()
	require.NotNil(t, m2)
	require.Same(t, m1, m2, "repeated Borrow must return same *plugins.Manager")
}

func TestCache_ReloadReturnsDifferentPointer(t *testing.T) {
	t.Parallel()
	pc := plugincache.New(disabledPluginsCfg())
	defer pc.Close()

	m1, rel1 := pc.Borrow()
	rel1()
	require.NotNil(t, m1)

	pc.Reload(disabledPluginsCfg())

	m2, rel2 := pc.Borrow()
	defer rel2()
	require.NotNil(t, m2)
	require.NotSame(t, m1, m2, "Borrow after Reload must return a different manager")

	require.NotPanics(t, func() { _ = m1.Close() }, "closing old manager must not panic")
	require.NotPanics(t, func() { pc.Close() })
}

func TestCache_ConcurrentBorrow(t *testing.T) {
	t.Parallel()
	const goroutines = 50
	pc := plugincache.New(disabledPluginsCfg())
	defer pc.Close()

	var wg sync.WaitGroup
	results := make([]bool, goroutines)
	for i := range goroutines {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			m, rel := pc.Borrow()
			defer rel()
			results[idx] = m != nil
		}(i)
	}
	wg.Wait()
	for i, ok := range results {
		require.True(t, ok, "goroutine %d got nil manager", i)
	}
}

func TestCache_RefcountDrain(t *testing.T) {
	t.Parallel()
	pc := plugincache.New(disabledPluginsCfg())
	defer pc.Close()

	oldMgr, releaseOld := pc.Borrow()
	require.NotNil(t, oldMgr)

	pc.Reload(disabledPluginsCfg())

	newMgr, releaseNew := pc.Borrow()
	defer releaseNew()
	require.NotNil(t, newMgr)
	require.NotSame(t, oldMgr, newMgr, "post-Reload Borrow must return new manager")

	require.NotPanics(t, func() { _ = oldMgr.Close() })
	require.NotPanics(t, func() { releaseOld() })
	require.NotPanics(t, func() { _ = oldMgr.Close() })
}
