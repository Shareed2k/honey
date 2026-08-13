package intercept

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"       // registers the "pgx" database/sql driver
	_ "github.com/ncruces/go-sqlite3/driver" // registers the "sqlite3" database/sql driver
)

// defaultSQLMaxOpenConns bounds the connection pool for the postgres driver.
// sqlite is forced to a single connection (see NewSQLStore) since it
// serializes writes at the file level and a shared-cache in-memory database
// only stays alive while at least one connection is open.
const defaultSQLMaxOpenConns = 10

// sqlite3Driver and pgxDriver are the only two database/sql driver names
// NewSQLStore accepts.
const (
	sqlite3Driver = "sqlite3"
	pgxDriver     = "pgx"
)

// SQLStore is a database/sql-backed SessionStore that works against either
// sqlite (driver "sqlite3") or postgres (driver "pgx"). The two backends
// share every query; only the schema's column types, the timestamp
// representation, and the placeholder syntax differ, and those differences
// are isolated behind isPostgres, encodeTime/scanSession, and rebind.
type SQLStore struct {
	db         *sql.DB
	isPostgres bool
}

var _ SessionStore = (*SQLStore)(nil)

// NewSQLStore opens a database/sql connection for driver ("sqlite3" or
// "pgx") and dsn, verifies it's reachable, and ensures the
// intercept_sessions table exists. dsn is never logged: it may contain
// credentials.
func NewSQLStore(ctx context.Context, driver, dsn string) (*SQLStore, error) {
	switch driver {
	case sqlite3Driver, pgxDriver:
	default:
		return nil, fmt.Errorf("intercept: unsupported session store driver %q", driver)
	}

	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open session store: %w", err)
	}

	if driver == sqlite3Driver {
		// A single connection serializes every query, which avoids
		// SQLITE_BUSY errors and keeps a shared-cache in-memory database
		// (used by tests) alive for the lifetime of the store.
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(defaultSQLMaxOpenConns)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping session store: %w", err)
	}

	s := &SQLStore{db: db, isPostgres: driver == pgxDriver}
	if err := s.ensureSchema(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensure session store schema: %w", err)
	}
	return s, nil
}

// ensureSchema creates the intercept_sessions table if it doesn't already
// exist. The column types differ by backend: sqlite uses TEXT/BLOB,
// postgres uses TEXT/BYTEA/TIMESTAMPTZ.
func (s *SQLStore) ensureSchema(ctx context.Context) error {
	ddl := `CREATE TABLE IF NOT EXISTS intercept_sessions (
	id TEXT PRIMARY KEY,
	actor TEXT NOT NULL,
	cluster TEXT NOT NULL,
	namespace TEXT NOT NULL,
	pod TEXT NOT NULL,
	container TEXT NOT NULL,
	modes TEXT NOT NULL,
	agent_image TEXT NOT NULL,
	token_hash BLOB NOT NULL,
	started_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
)`
	if s.isPostgres {
		ddl = `CREATE TABLE IF NOT EXISTS intercept_sessions (
	id TEXT PRIMARY KEY,
	actor TEXT NOT NULL,
	cluster TEXT NOT NULL,
	namespace TEXT NOT NULL,
	pod TEXT NOT NULL,
	container TEXT NOT NULL,
	modes TEXT NOT NULL,
	agent_image TEXT NOT NULL,
	token_hash BYTEA NOT NULL,
	started_at TIMESTAMPTZ NOT NULL,
	expires_at TIMESTAMPTZ NOT NULL
)`
	}
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("create intercept_sessions table: %w", err)
	}
	return nil
}

// Save upserts ps: a session with the same ID is replaced. All values are
// bound as query parameters (never string-concatenated).
func (s *SQLStore) Save(ctx context.Context, ps PersistedSession) error {
	modes, err := json.Marshal(ps.Modes)
	if err != nil {
		return fmt.Errorf("marshal session modes: %w", err)
	}

	query := s.rebind(`
INSERT INTO intercept_sessions
	(id, actor, cluster, namespace, pod, container, modes, agent_image, token_hash, started_at, expires_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	actor = excluded.actor,
	cluster = excluded.cluster,
	namespace = excluded.namespace,
	pod = excluded.pod,
	container = excluded.container,
	modes = excluded.modes,
	agent_image = excluded.agent_image,
	token_hash = excluded.token_hash,
	started_at = excluded.started_at,
	expires_at = excluded.expires_at`)

	_, err = s.db.ExecContext(ctx, query,
		ps.ID, ps.Actor, ps.Cluster, ps.Namespace, ps.Pod, ps.Container,
		string(modes), ps.AgentImage, ps.TokenHash,
		s.encodeTime(ps.StartedAt), s.encodeTime(ps.ExpiresAt),
	)
	if err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	return nil
}

// Get returns the session with the given id. A missing id is not an error:
// it returns a zero PersistedSession and ok == false.
func (s *SQLStore) Get(ctx context.Context, id string) (PersistedSession, bool, error) {
	query := s.rebind(`
SELECT id, actor, cluster, namespace, pod, container, modes, agent_image, token_hash, started_at, expires_at
FROM intercept_sessions WHERE id = ?`)

	ps, err := s.scanSession(s.db.QueryRowContext(ctx, query, id))
	if errors.Is(err, sql.ErrNoRows) {
		return PersistedSession{}, false, nil
	}
	if err != nil {
		return PersistedSession{}, false, fmt.Errorf("get session: %w", err)
	}
	return ps, true, nil
}

// Delete removes the session with the given id. Deleting a missing id is
// not an error.
func (s *SQLStore) Delete(ctx context.Context, id string) error {
	query := s.rebind(`DELETE FROM intercept_sessions WHERE id = ?`)
	if _, err := s.db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// List returns every persisted session.
func (s *SQLStore) List(ctx context.Context) ([]PersistedSession, error) {
	query := `
SELECT id, actor, cluster, namespace, pod, container, modes, agent_image, token_hash, started_at, expires_at
FROM intercept_sessions`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	out := make([]PersistedSession, 0)
	for rows.Next() {
		ps, err := s.scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		out = append(out, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return out, nil
}

// Close releases the underlying database/sql connection pool.
func (s *SQLStore) Close() error {
	return s.db.Close()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting Get and
// List share one scan implementation.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanSession scans one intercept_sessions row, in the exact column order
// used by the Get/List queries, into a PersistedSession: modes is
// JSON-decoded and the timestamp columns are parsed/normalized to UTC so
// they round-trip exactly what Save stored.
func (s *SQLStore) scanSession(row rowScanner) (PersistedSession, error) {
	var (
		ps        PersistedSession
		modesJSON string
	)

	if s.isPostgres {
		// postgres TIMESTAMPTZ scans straight into time.Time.
		var startedAt, expiresAt time.Time
		if err := row.Scan(
			&ps.ID, &ps.Actor, &ps.Cluster, &ps.Namespace, &ps.Pod, &ps.Container,
			&modesJSON, &ps.AgentImage, &ps.TokenHash, &startedAt, &expiresAt,
		); err != nil {
			return PersistedSession{}, err
		}
		ps.StartedAt = startedAt.UTC()
		ps.ExpiresAt = expiresAt.UTC()
	} else {
		// sqlite TEXT holds the RFC3339Nano string Save wrote; parse it back.
		var startedAt, expiresAt string
		if err := row.Scan(
			&ps.ID, &ps.Actor, &ps.Cluster, &ps.Namespace, &ps.Pod, &ps.Container,
			&modesJSON, &ps.AgentImage, &ps.TokenHash, &startedAt, &expiresAt,
		); err != nil {
			return PersistedSession{}, err
		}
		started, err := time.Parse(time.RFC3339Nano, startedAt)
		if err != nil {
			return PersistedSession{}, fmt.Errorf("parse started_at: %w", err)
		}
		expires, err := time.Parse(time.RFC3339Nano, expiresAt)
		if err != nil {
			return PersistedSession{}, fmt.Errorf("parse expires_at: %w", err)
		}
		ps.StartedAt = started.UTC()
		ps.ExpiresAt = expires.UTC()
	}

	if err := json.Unmarshal([]byte(modesJSON), &ps.Modes); err != nil {
		return PersistedSession{}, fmt.Errorf("unmarshal session modes: %w", err)
	}
	return ps, nil
}

// encodeTime returns the value to bind for a timestamp column: a native
// time.Time for postgres's TIMESTAMPTZ, or an RFC3339Nano UTC string for
// sqlite's TEXT.
func (s *SQLStore) encodeTime(t time.Time) any {
	if s.isPostgres {
		return t.UTC()
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// rebind rewrites a query written with "?" placeholders into the syntax the
// driver expects: postgres (pgx) requires "$1", "$2", ...; sqlite keeps "?"
// unchanged.
func (s *SQLStore) rebind(query string) string {
	if !s.isPostgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
