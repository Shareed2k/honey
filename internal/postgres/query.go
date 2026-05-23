package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// QueryOpts configures a postgres query.
type QueryOpts struct {
	Timeout  time.Duration
	Readonly bool
	PluginID string
	HostName string
	DryRun   bool
}

// QueryResult holds rows from a SELECT-style query.
type QueryResult struct {
	Rows []map[string]any
}

// Query runs a read-only SQL query via pgx.
func Query(ctx context.Context, pools *PoolManager, dsn, sql string, args []any, opts QueryOpts) (QueryResult, error) {
	if err := ValidateReadonlySQL(sql); err != nil {
		return QueryResult{}, err
	}
	if err := ValidateParamPlaceholders(sql, len(args)); err != nil {
		return QueryResult{}, err
	}
	if opts.DryRun {
		LogAudit(AuditEvent{PluginID: opts.PluginID, HostName: opts.HostName, Action: "query", SQL: sql, Readonly: true, DryRun: true})
		return QueryResult{}, nil
	}
	qctx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		qctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	pool, err := pools.Acquire(qctx, dsn)
	if err != nil {
		return QueryResult{}, err
	}
	rows, err := pool.Query(qctx, sql, args...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("postgres: query: %w", err)
	}
	defer rows.Close()
	fieldDescs := rows.FieldDescriptions()
	var out []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return QueryResult{}, fmt.Errorf("postgres: scan row: %w", err)
		}
		row := make(map[string]any, len(fieldDescs))
		for i, fd := range fieldDescs {
			row[fd.Name] = jsonSafeValue(vals[i])
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, fmt.Errorf("postgres: rows: %w", err)
	}
	LogAudit(AuditEvent{
		PluginID: opts.PluginID, HostName: opts.HostName, Action: "query", SQL: sql,
		Readonly: opts.Readonly, RowCount: len(out),
	})
	return QueryResult{Rows: out}, nil
}

func jsonSafeValue(v any) any {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case pgtype.Numeric:
		if !x.Valid {
			return nil
		}
		f, err := x.Float64Value()
		if err == nil && f.Valid {
			return f.Float64
		}
		return x.Int.String()
	case []byte:
		return string(x)
	default:
		return v
	}
}

// ExecOpts configures a postgres exec.
type ExecOpts struct {
	Timeout  time.Duration
	Readonly bool
	PluginID string
	HostName string
	DryRun   bool
}

// ExecResult holds rows affected from a write statement.
type ExecResult struct {
	RowsAffected int64
}

// Exec runs INSERT/UPDATE/DDL via pgx.
func Exec(ctx context.Context, pools *PoolManager, dsn, sql string, args []any, opts ExecOpts) (ExecResult, error) {
	if opts.Readonly && !IsReadonlySQL(sql) {
		return ExecResult{}, fmt.Errorf("postgres: readonly mode rejects write sql")
	}
	if err := ValidateParamPlaceholders(sql, len(args)); err != nil {
		return ExecResult{}, err
	}
	if opts.DryRun {
		LogAudit(AuditEvent{PluginID: opts.PluginID, HostName: opts.HostName, Action: "exec", SQL: sql, Readonly: opts.Readonly, DryRun: true})
		return ExecResult{}, nil
	}
	qctx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		qctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	pool, err := pools.Acquire(qctx, dsn)
	if err != nil {
		return ExecResult{}, err
	}
	tag, err := pool.Exec(qctx, sql, args...)
	if err != nil {
		return ExecResult{}, fmt.Errorf("postgres: exec: %w", err)
	}
	n := tag.RowsAffected()
	LogAudit(AuditEvent{
		PluginID: opts.PluginID, HostName: opts.HostName, Action: "exec", SQL: sql,
		Readonly: opts.Readonly, RowsAffected: n,
	})
	return ExecResult{RowsAffected: n}, nil
}

// Ping verifies connectivity without running user SQL.
func Ping(ctx context.Context, pools *PoolManager, dsn string, timeout time.Duration) error {
	qctx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		qctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	pool, err := pools.Acquire(qctx, dsn)
	if err != nil {
		return err
	}
	return pool.Ping(qctx)
}
