//go:build !wasip1 && !wasm

package main

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
)

func TestRunSQLiteStepQueryReturnsRows(t *testing.T) {
	dbPath := seedSQLiteDB(t)
	cfg := mustJSON(t, sqliteConfig{
		DSN:      "file:" + filepath.ToSlash(dbPath) + "?mode=ro",
		SQL:      "SELECT id, name, active FROM users WHERE active = ? ORDER BY id",
		Params:   []any{true},
		Readonly: boolPtr(true),
	})

	out := runSQLiteStep(executeStepInput{Action: "query", Config: cfg, Execute: true})

	if !out.Success {
		t.Fatalf("query failed: %s", out.Err)
	}
	if out.Changed {
		t.Fatalf("query changed = true, want false")
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out.Stdout), &rows); err != nil {
		t.Fatalf("stdout is not row JSON: %v\n%s", err, out.Stdout)
	}
	if len(rows) != 1 {
		t.Fatalf("rows len = %d, want 1: %#v", len(rows), rows)
	}
	if rows[0]["name"] != "alice" {
		t.Fatalf("name = %#v, want alice", rows[0]["name"])
	}
}

func TestRunSQLiteStepExecChangesRows(t *testing.T) {
	dbPath := seedSQLiteDB(t)
	cfg := mustJSON(t, sqliteConfig{
		DSN:    "file:" + filepath.ToSlash(dbPath) + "?mode=rw",
		SQL:    "INSERT INTO users(name, active) VALUES (?, ?)",
		Params: []any{"carol", true},
	})

	out := runSQLiteStep(executeStepInput{Action: "exec", Config: cfg, Execute: true})

	if !out.Success {
		t.Fatalf("exec failed: %s", out.Err)
	}
	if !out.Changed {
		t.Fatalf("exec changed = false, want true")
	}
	var payload struct {
		RowsAffected int64 `json:"rows_affected"`
	}
	if err := json.Unmarshal([]byte(out.Stdout), &payload); err != nil {
		t.Fatalf("stdout is not exec JSON: %v\n%s", err, out.Stdout)
	}
	if payload.RowsAffected != 1 {
		t.Fatalf("rows_affected = %d, want 1", payload.RowsAffected)
	}

	db := openTestDB(t, dbPath)
	defer db.Close()
	var count int
	if err := db.QueryRow("SELECT count(*) FROM users WHERE name = 'carol'").Scan(&count); err != nil {
		t.Fatalf("count carol: %v", err)
	}
	if count != 1 {
		t.Fatalf("carol rows = %d, want 1", count)
	}
}

func TestRunSQLiteStepExecRejectsReadonly(t *testing.T) {
	dbPath := seedSQLiteDB(t)
	cfg := mustJSON(t, sqliteConfig{
		DSN:      "file:" + filepath.ToSlash(dbPath) + "?mode=rw",
		SQL:      "INSERT INTO users(name, active) VALUES ('carol', 1)",
		Readonly: boolPtr(true),
	})

	out := runSQLiteStep(executeStepInput{Action: "exec", Config: cfg, Execute: true})

	if out.Success {
		t.Fatal("exec succeeded with readonly=true")
	}
	if !strings.Contains(strings.ToLower(out.Err), "readonly") {
		t.Fatalf("err = %q, want readonly error", out.Err)
	}
}

func TestRunSQLiteStepDryRunDoesNotOpenDatabase(t *testing.T) {
	cfg := mustJSON(t, sqliteConfig{
		DSN: "file:/definitely/missing/sqlite.db?mode=ro",
		SQL: "SELECT 1",
	})

	query := runSQLiteStep(executeStepInput{Action: "query", Config: cfg, Execute: false})
	if !query.Success || query.Changed {
		t.Fatalf("query dry-run = %+v, want success without change", query)
	}
	if !strings.Contains(query.Stdout, "would run sqlite query") {
		t.Fatalf("query dry-run stdout = %q", query.Stdout)
	}

	exec := runSQLiteStep(executeStepInput{Action: "exec", Config: cfg, Execute: false})
	if !exec.Success || !exec.Changed {
		t.Fatalf("exec dry-run = %+v, want success with change", exec)
	}
	if !strings.Contains(exec.Stdout, "would run sqlite exec") {
		t.Fatalf("exec dry-run stdout = %q", exec.Stdout)
	}
}

func TestRunSQLiteStepUnknownAction(t *testing.T) {
	out := runSQLiteStep(executeStepInput{Action: "vacuum", Execute: true})

	if out.Success {
		t.Fatal("unknown action succeeded")
	}
	if !strings.Contains(out.Err, "unknown action vacuum") {
		t.Fatalf("err = %q", out.Err)
	}
}

func seedSQLiteDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db := openTestDB(t, dbPath)
	defer db.Close()
	_, err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			active INTEGER NOT NULL
		);
		INSERT INTO users(name, active) VALUES ('alice', 1), ('bob', 0);
	`)
	if err != nil {
		t.Fatalf("seed sqlite: %v", err)
	}
	return dbPath
}

func openTestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dbPath)+"?mode=rwc")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	return db
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return b
}

func boolPtr(v bool) *bool {
	return &v
}
