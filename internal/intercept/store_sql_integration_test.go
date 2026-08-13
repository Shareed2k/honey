//go:build integration

package intercept

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestSQLStore_Postgres_Conformance runs the same shared SessionStore
// conformance suite as TestSQLStore_SQLite_Conformance, but against a real
// postgres backend started as an ephemeral testcontainer. It requires a
// Docker daemon and only runs with `-tags integration`; without Docker it
// skips rather than failing.
func TestSQLStore_Postgres_Conformance(t *testing.T) {
	ctx := context.Background()

	c, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage("postgres:16-alpine"),
		tcpostgres.WithDatabase("honey_intercept_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("start postgres testcontainer: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(c); err != nil {
			t.Logf("terminate postgres testcontainer: %v", err)
		}
	})

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	runStoreConformance(t, func(t *testing.T) SessionStore {
		s, err := NewSQLStore(context.Background(), "pgx", dsn)
		require.NoError(t, err)
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}
