package engine

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestRecipeInterceptCoordinator_registerLookupCount covers the basic
// Register/Lookup/Count semantics: an unregistered id misses, a registered
// one is found, and Count reflects the live registration size (the number
// max_sessions gates against).
func TestRecipeInterceptCoordinator_registerLookupCount(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	coord := NewRecipeInterceptCoordinator()
	assert.Equal(t, 0, coord.Count())

	_, ok := coord.Lookup("missing")
	assert.False(t, ok)

	live1 := &fakeInterceptLive{}
	live2 := &fakeInterceptLive{}
	coord.Register("s1", live1)
	coord.Register("s2", live2)
	assert.Equal(t, 2, coord.Count())

	got, ok := coord.Lookup("s1")
	require.True(t, ok)
	assert.Same(t, live1, got)

	coord.Close()
}

// TestRecipeInterceptCoordinator_closeExactlyOnceReverseOrder proves Close
// tears every registered session down exactly once, in reverse registration
// order, and that a second Close is a no-op (idempotent).
func TestRecipeInterceptCoordinator_closeExactlyOnceReverseOrder(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	coord := NewRecipeInterceptCoordinator()
	log := &fakeCloseLog{}
	live1 := &fakeInterceptLive{name: "a", log: log}
	live2 := &fakeInterceptLive{name: "b", log: log}
	live3 := &fakeInterceptLive{name: "c", log: log}
	coord.Register("a", live1)
	coord.Register("b", live2)
	coord.Register("c", live3)

	coord.Close()
	assert.Equal(t, []string{"c", "b", "a"}, log.snapshot(), "Close must tear sessions down in reverse registration order")
	assert.Equal(t, []string{interceptCoordCloseReason}, live1.closeReasons())
	assert.Equal(t, []string{interceptCoordCloseReason}, live2.closeReasons())
	assert.Equal(t, []string{interceptCoordCloseReason}, live3.closeReasons())

	// Idempotent: a second Close must not touch any session again.
	coord.Close()
	assert.Equal(t, []string{"c", "b", "a"}, log.snapshot())
	assert.Len(t, live1.closeReasons(), 1)

	assert.Equal(t, 0, coord.Count())
	_, ok := coord.Lookup("a")
	assert.False(t, ok, "a closed coordinator must not keep serving lookups")
}

// TestRecipeInterceptCoordinator_registerAfterCloseClosesImmediately proves a
// Register racing a concurrent run-teardown Close never leaks: the session
// registered too late is closed on the spot instead of being retained.
func TestRecipeInterceptCoordinator_registerAfterCloseClosesImmediately(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	coord := NewRecipeInterceptCoordinator()
	coord.Close()

	late := &fakeInterceptLive{}
	coord.Register("late", late)

	assert.Equal(t, []string{interceptCoordCloseReason}, late.closeReasons())
	assert.Equal(t, 0, coord.Count())
	_, ok := coord.Lookup("late")
	assert.False(t, ok)
}

// TestRecipeInterceptCoordinator_nilSafe proves every method is safe to call
// on a nil *RecipeInterceptCoordinator (mirrors RecipeTunnelCoordinator's
// nil-safety, exercised by callers that may not have one configured), and
// that Register on a nil coordinator still closes the session it was handed
// rather than silently dropping it.
func TestRecipeInterceptCoordinator_nilSafe(t *testing.T) {
	defer goleak.VerifyNone(t, interceptGoleakOpts()...)

	var coord *RecipeInterceptCoordinator
	assert.Equal(t, 0, coord.Count())
	_, ok := coord.Lookup("x")
	assert.False(t, ok)
	assert.NotPanics(t, func() { coord.Close() })

	live := &fakeInterceptLive{}
	assert.NotPanics(t, func() { coord.Register("x", live) })
	assert.Equal(t, []string{interceptCoordCloseReason}, live.closeReasons())
}
