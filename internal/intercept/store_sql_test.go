package intercept

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSQLStore_SQLite_Conformance runs the shared SessionStore conformance
// suite against an in-memory sqlite database reached through database/sql,
// proving sqlStore satisfies the exact same contract as memStore.
func TestSQLStore_SQLite_Conformance(t *testing.T) {
	runStoreConformance(t, func(t *testing.T) SessionStore {
		s, err := newSQLStore(context.Background(), "sqlite3", "file::memory:?cache=shared")
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// TestNewSQLStore_UnsupportedDriver proves an unknown driver name fails
// closed instead of silently falling back to some default.
func TestNewSQLStore_UnsupportedDriver(t *testing.T) {
	_, err := newSQLStore(context.Background(), "mysql", "unused")
	require.Error(t, err)
}

// TestSQLStore_Rebind proves the "?" -> "$N" rewrite only fires for the pgx
// driver; sqlite keeps "?" placeholders as-is.
func TestSQLStore_Rebind(t *testing.T) {
	const query = "SELECT * FROM intercept_sessions WHERE id = ? AND actor = ?"

	pg := &sqlStore{isPostgres: true}
	require.Equal(t, "SELECT * FROM intercept_sessions WHERE id = $1 AND actor = $2", pg.rebind(query))

	lite := &sqlStore{isPostgres: false}
	require.Equal(t, query, lite.rebind(query))
}
