package postgres

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/shareed2k/honey/internal/safepath"
)

// MigrateOpts configures schema migration.
type MigrateOpts struct {
	Timeout  time.Duration
	Readonly bool
	PluginID string
	HostName string
	DryRun   bool
}

// MigrateResult summarizes applied migrations.
type MigrateResult struct {
	Applied []string
	Stdout  string
}

// Migrate applies ordered .sql files inside migrationsDir.
func Migrate(ctx context.Context, pools *PoolManager, dsn, migrationsDir string, files []string, opts MigrateOpts) (MigrateResult, error) {
	if opts.Readonly {
		return MigrateResult{}, fmt.Errorf("postgres: readonly mode rejects migrate")
	}
	paths, err := resolveMigrationFiles(migrationsDir, files)
	if err != nil {
		return MigrateResult{}, err
	}
	if len(paths) == 0 {
		return MigrateResult{}, fmt.Errorf("postgres: no migration files found")
	}
	if opts.DryRun {
		msg := "would apply migrations: " + strings.Join(paths, ", ")
		LogAudit(AuditEvent{PluginID: opts.PluginID, HostName: opts.HostName, Action: "migrate", SQL: msg, DryRun: true})
		return MigrateResult{Applied: paths, Stdout: msg}, nil
	}
	qctx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		qctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	pool, err := pools.Acquire(qctx, dsn)
	if err != nil {
		return MigrateResult{}, err
	}
	conn, err := pool.Acquire(qctx)
	if err != nil {
		return MigrateResult{}, err
	}
	defer conn.Release()
	tx, err := conn.BeginTx(qctx, pgx.TxOptions{})
	if err != nil {
		return MigrateResult{}, fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(qctx) }()
	var applied []string
	for _, path := range paths {
		b, err := safepath.ReadFile(path)
		if err != nil {
			return MigrateResult{}, fmt.Errorf("postgres: read migration %s: %w", path, err)
		}
		sql := strings.TrimSpace(string(b))
		if sql == "" {
			continue
		}
		if _, err := tx.Exec(qctx, sql); err != nil {
			return MigrateResult{}, fmt.Errorf("postgres: migration %s: %w", path, err)
		}
		applied = append(applied, path)
		LogAudit(AuditEvent{PluginID: opts.PluginID, HostName: opts.HostName, Action: "migrate", SQL: sql, RowsAffected: 1})
	}
	if err := tx.Commit(qctx); err != nil {
		return MigrateResult{}, fmt.Errorf("postgres: commit: %w", err)
	}
	stdout := fmt.Sprintf("applied %d migration(s)", len(applied))
	return MigrateResult{Applied: applied, Stdout: stdout}, nil
}

func resolveMigrationFiles(migrationsDir string, explicit []string) ([]string, error) {
	if len(explicit) > 0 {
		out := make([]string, 0, len(explicit))
		for _, f := range explicit {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			if !filepath.IsAbs(f) {
				if strings.TrimSpace(migrationsDir) == "" {
					return nil, fmt.Errorf("postgres: migrations_dir required for relative file %q", f)
				}
				f = filepath.Join(migrationsDir, f)
			}
			f = filepath.Clean(f)
			if !strings.HasSuffix(strings.ToLower(f), ".sql") {
				return nil, fmt.Errorf("postgres: migration %q must be a .sql file", f)
			}
			out = append(out, f)
		}
		sort.Strings(out)
		return out, nil
	}
	dir := strings.TrimSpace(migrationsDir)
	if dir == "" {
		return nil, fmt.Errorf("postgres: migrations_dir or files is required")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("postgres: read migrations dir: %w", err)
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sql") {
			continue
		}
		paths = append(paths, filepath.Join(dir, e.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}
