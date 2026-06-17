//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/postgres"
)

func TestPostgresQuery_Select(t *testing.T) {
	dsn := startPostgres(t)
	pools := postgres.NewPoolManager()

	res, err := postgres.Query(
		context.Background(), pools, dsn,
		"SELECT 1+1 AS result", nil,
		postgres.QueryOpts{Timeout: 5 * time.Second, Readonly: true},
	)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(res.Rows))
	}
	if _, ok := res.Rows[0]["result"]; !ok {
		t.Fatal("expected column 'result' in row")
	}
}

func TestPostgresQuery_ReadOnly_Enforcement(t *testing.T) {
	dsn := startPostgres(t)
	pools := postgres.NewPoolManager()

	// Read-only query must succeed.
	_, err := postgres.Query(
		context.Background(), pools, dsn,
		"SELECT current_database()", nil,
		postgres.QueryOpts{Timeout: 5 * time.Second, Readonly: true},
	)
	if err != nil {
		t.Fatalf("select failed: %v", err)
	}

	// DDL must be rejected by the readonly validator before hitting the DB.
	_, err = postgres.Query(
		context.Background(), pools, dsn,
		"DROP TABLE IF EXISTS does_not_exist", nil,
		postgres.QueryOpts{Timeout: 5 * time.Second, Readonly: true},
	)
	if err == nil {
		t.Fatal("expected error for DDL with Readonly=true, got nil")
	}
}

func TestPostgresQuery_Timeout(t *testing.T) {
	dsn := startPostgres(t)
	pools := postgres.NewPoolManager()

	_, err := postgres.Query(
		context.Background(), pools, dsn,
		"SELECT pg_sleep(5)", nil,
		postgres.QueryOpts{Timeout: 1 * time.Millisecond, Readonly: true},
	)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}

func TestPostgresPool_Reuse(t *testing.T) {
	dsn := startPostgres(t)
	pools := postgres.NewPoolManager()

	for i := range 2 {
		_, err := postgres.Query(
			context.Background(), pools, dsn,
			"SELECT 42", nil,
			postgres.QueryOpts{Timeout: 5 * time.Second, Readonly: true},
		)
		if err != nil {
			t.Fatalf("query %d failed: %v", i, err)
		}
	}
}

func TestPostgresValidateReadonlySQL(t *testing.T) {
	cases := []struct {
		sql     string
		wantErr bool
	}{
		{"SELECT 1", false},
		{"select * from pg_stat_activity", false},
		{"DROP TABLE foo", true},
		{"INSERT INTO foo VALUES (1)", true},
		{"UPDATE foo SET x=1", true},
		{"DELETE FROM foo", true},
		{"CREATE TABLE foo (id int)", true},
	}
	for _, tc := range cases {
		err := postgres.ValidateReadonlySQL(tc.sql)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateReadonlySQL(%q): wantErr=%v got %v", tc.sql, tc.wantErr, err)
		}
	}
}
