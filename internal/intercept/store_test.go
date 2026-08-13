package intercept

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// runStoreConformance is the shared behavioral contract every SessionStore
// implementation must satisfy. Later tasks (the sql-backed store) reuse it
// unchanged, so it must not assume anything beyond the SessionStore interface
// itself.
func runStoreConformance(t *testing.T, newStore func(t *testing.T) SessionStore) {
	t.Helper()
	ctx := context.Background()
	s := newStore(t)

	ps := PersistedSession{
		ID: "s1", Actor: "alice", Cluster: "prod", Namespace: "n", Pod: "p",
		Container: "mogate-abc", Modes: []string{"egress"}, AgentImage: "img",
		TokenHash: []byte{1, 2, 3},
		StartedAt: time.Unix(1000, 0).UTC(), ExpiresAt: time.Unix(2000, 0).UTC(),
	}
	require.NoError(t, s.Save(ctx, ps))

	got, ok, err := s.Get(ctx, "s1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, ps, got)

	_, ok, err = s.Get(ctx, "nope")
	require.NoError(t, err)
	require.False(t, ok) // not found ⇒ (_, false, nil)

	ps.Actor = "bob"
	require.NoError(t, s.Save(ctx, ps)) // upsert
	got, _, _ = s.Get(ctx, "s1")
	require.Equal(t, "bob", got.Actor)

	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)

	require.NoError(t, s.Delete(ctx, "s1"))
	_, ok, _ = s.Get(ctx, "s1")
	require.False(t, ok)
	require.NoError(t, s.Delete(ctx, "s1")) // delete-missing is not an error
}

// TestMemStore_Conformance runs the shared conformance suite against memStore.
func TestMemStore_Conformance(t *testing.T) {
	runStoreConformance(t, func(_ *testing.T) SessionStore { return newMemStore() })
}

// TestMemStore_DeepCopy proves that memStore never shares backing arrays with
// its callers: mutating the slice fields of a PersistedSession passed to Save,
// or of one returned by Get/List, must never change what the store holds.
func TestMemStore_DeepCopy(t *testing.T) {
	ctx := context.Background()
	s := newMemStore()

	modes := []string{"egress"}
	tokenHash := []byte{1, 2, 3}
	ps := PersistedSession{
		ID: "s1", Actor: "alice", Modes: modes, TokenHash: tokenHash,
		StartedAt: time.Unix(1000, 0).UTC(), ExpiresAt: time.Unix(2000, 0).UTC(),
	}
	require.NoError(t, s.Save(ctx, ps))

	// Mutating the caller's input slices after Save must not affect the
	// stored copy.
	modes[0] = "corrupted"
	tokenHash[0] = 0xff

	got, ok, err := s.Get(ctx, "s1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"egress"}, got.Modes)
	require.Equal(t, []byte{1, 2, 3}, got.TokenHash)

	// Mutating a slice returned by Get must not affect the stored state.
	got.Modes[0] = "also-corrupted"
	got.TokenHash[0] = 0xee

	again, ok, err := s.Get(ctx, "s1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"egress"}, again.Modes)
	require.Equal(t, []byte{1, 2, 3}, again.TokenHash)

	// Mutating a slice returned by List must not affect the stored state.
	all, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	all[0].Modes[0] = "list-corrupted"
	all[0].TokenHash[0] = 0xdd

	final, ok, err := s.Get(ctx, "s1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, []string{"egress"}, final.Modes)
	require.Equal(t, []byte{1, 2, 3}, final.TokenHash)
}
