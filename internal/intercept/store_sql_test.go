package intercept

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSQLStore_SQLite_Conformance runs the shared SessionStore conformance
// suite against an in-memory sqlite database reached through database/sql,
// proving SQLStore satisfies the exact same contract as memStore.
func TestSQLStore_SQLite_Conformance(t *testing.T) {
	runStoreConformance(t, func(t *testing.T) SessionStore {
		s, err := NewSQLStore(context.Background(), "sqlite3", "file::memory:?cache=shared")
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

// TestNewSQLStore_UnsupportedDriver proves an unknown driver name fails
// closed instead of silently falling back to some default.
func TestNewSQLStore_UnsupportedDriver(t *testing.T) {
	_, err := NewSQLStore(context.Background(), "mysql", "unused")
	require.Error(t, err)
}

// TestNewSQLStore_SQLiteFilePermRestricted proves an on-disk sqlite session
// store is created readable only by its owner: the database holds session
// metadata and a token hash, so it must not be group/world-readable
// regardless of the process umask.
func TestNewSQLStore_SQLiteFilePermRestricted(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "sessions.db")
	s, err := NewSQLStore(context.Background(), "sqlite3", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	fi, err := os.Stat(dsn)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// TestSqliteFilePath proves the dsn -> filesystem-path extraction used to
// restrict permissions: plain paths pass through, "file:" URIs are
// unwrapped and any query suffix dropped, and the in-memory special forms
// resolve to "" (no real file to chmod).
func TestSqliteFilePath(t *testing.T) {
	require.Equal(t, "/tmp/sessions.db", sqliteFilePath("/tmp/sessions.db"))
	require.Equal(t, "/tmp/sessions.db", sqliteFilePath("file:/tmp/sessions.db"))
	require.Equal(t, "/tmp/sessions.db", sqliteFilePath("file:/tmp/sessions.db?_fk=1"))
	require.Equal(t, "", sqliteFilePath(":memory:"))
	require.Equal(t, "", sqliteFilePath("file::memory:?cache=shared"))
}

// TestSQLStore_Rebind proves the "?" -> "$N" rewrite only fires for the pgx
// driver; sqlite keeps "?" placeholders as-is.
func TestSQLStore_Rebind(t *testing.T) {
	const query = "SELECT * FROM intercept_sessions WHERE id = ? AND actor = ?"

	pg := &SQLStore{isPostgres: true}
	require.Equal(t, "SELECT * FROM intercept_sessions WHERE id = $1 AND actor = $2", pg.rebind(query))

	lite := &SQLStore{isPostgres: false}
	require.Equal(t, query, lite.rebind(query))
}
