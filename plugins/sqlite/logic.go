// Package main implements the Honey SQLite WASM plugin.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shareed2k/honey/pkg/pluginpdk"

	_ "github.com/ncruces/go-sqlite3/driver"
)

const defaultSQLiteTimeoutMS = 30000

func runSQLiteStep(in executeStepInput) executeStepOutput {
	action := strings.ToLower(strings.TrimSpace(in.Action))
	if !knownAction(action) {
		return executeStepOutput{Success: false, Err: "unknown action " + in.Action}
	}

	cfg, err := parseSQLiteConfig(in.Config)
	if err != nil {
		return executeStepOutput{Success: false, Err: err.Error()}
	}
	if !in.Execute {
		return sqliteDryRun(action)
	}

	switch action {
	case "query":
		return runSQLiteQuery(cfg, in.Host)
	case "exec":
		return runSQLiteExec(cfg, in.Host)
	default:
		return executeStepOutput{Success: false, Err: "unknown action " + in.Action}
	}
}

func knownAction(action string) bool {
	switch action {
	case "query", "exec":
		return true
	default:
		return false
	}
}

func parseSQLiteConfig(raw []byte) (sqliteConfig, error) {
	var cfg sqliteConfig
	if len(raw) == 0 {
		return cfg, fmt.Errorf("plugin config is empty")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse plugin config: %w", err)
	}
	cfg.DSN = strings.TrimSpace(cfg.DSN)
	cfg.SQL = strings.TrimSpace(cfg.SQL)
	if cfg.DSN == "" {
		return cfg, fmt.Errorf("config.dsn is required")
	}
	if cfg.SQL == "" {
		return cfg, fmt.Errorf("config.sql is required")
	}
	return cfg, nil
}

func sqliteDryRun(action string) executeStepOutput {
	return executeStepOutput{
		Success: true,
		Changed: action == "exec",
		Stdout:  "would run sqlite " + action,
	}
}

func runSQLiteQuery(cfg sqliteConfig, host []byte) executeStepOutput {
	ctx, cancel := sqliteContext(cfg.TimeoutMS)
	defer cancel()

	db, err := sql.Open("sqlite3", cfg.DSN)
	if err != nil {
		return executeStepOutput{Success: false, Err: "open sqlite: " + err.Error()}
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, cfg.SQL, cfg.Params...)
	if err != nil {
		return executeStepOutput{Success: false, Err: "query sqlite: " + err.Error()}
	}
	defer rows.Close()

	result, err := scanRows(rows)
	if err != nil {
		return executeStepOutput{Success: false, Err: err.Error()}
	}
	stdout, err := marshalStdout(result)
	if err != nil {
		return executeStepOutput{Success: false, Err: err.Error()}
	}
	if err := storeKV(cfg, host, stdout); err != nil {
		return executeStepOutput{Success: false, Err: err.Error()}
	}
	return executeStepOutput{Success: true, Changed: false, Stdout: stdout}
}

func runSQLiteExec(cfg sqliteConfig, host []byte) executeStepOutput {
	if cfg.Readonly != nil && *cfg.Readonly {
		return executeStepOutput{Success: false, Err: "sqlite exec refused because config.readonly is true"}
	}
	ctx, cancel := sqliteContext(cfg.TimeoutMS)
	defer cancel()

	db, err := sql.Open("sqlite3", cfg.DSN)
	if err != nil {
		return executeStepOutput{Success: false, Err: "open sqlite: " + err.Error()}
	}
	defer db.Close()

	res, err := db.ExecContext(ctx, cfg.SQL, cfg.Params...)
	if err != nil {
		return executeStepOutput{Success: false, Err: "exec sqlite: " + err.Error()}
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		rowsAffected = 0
	}
	stdout, err := marshalStdout(map[string]any{"rows_affected": rowsAffected})
	if err != nil {
		return executeStepOutput{Success: false, Err: err.Error()}
	}
	if err := storeKV(cfg, host, stdout); err != nil {
		return executeStepOutput{Success: false, Err: err.Error()}
	}
	return executeStepOutput{Success: true, Changed: true, Stdout: stdout}
}

func sqliteContext(timeoutMS int) (context.Context, context.CancelFunc) {
	if timeoutMS <= 0 {
		timeoutMS = defaultSQLiteTimeoutMS
	}
	return context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
}

func scanRows(rows *sql.Rows) ([]map[string]any, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read sqlite columns: %w", err)
	}
	var out []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		scan := make([]any, len(cols))
		for i := range values {
			scan[i] = &values[i]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, fmt.Errorf("scan sqlite row: %w", err)
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = normalizeSQLiteValue(values[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sqlite rows: %w", err)
	}
	return out, nil
}

func normalizeSQLiteValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return string(x)
	default:
		return x
	}
}

func marshalStdout(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("encode sqlite output: %w", err)
	}
	return string(b), nil
}

func storeKV(cfg sqliteConfig, host []byte, stdout string) error {
	key := strings.TrimSpace(cfg.KVKey)
	if key == "" {
		return nil
	}
	if cfg.KVKeyPerHost {
		name := hostName(host)
		if name != "" {
			key += "_" + name
		}
	}
	if err := pluginpdk.KVPut(key, stdout); err != nil {
		return fmt.Errorf("store sqlite output in kv: %w", err)
	}
	return nil
}

func hostName(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var h struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		return ""
	}
	return strings.TrimSpace(h.Name)
}
