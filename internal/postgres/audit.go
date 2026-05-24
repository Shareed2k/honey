// Package postgres runs host-mediated PostgreSQL queries via pgx on the operator.
package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"go.uber.org/zap"
)

// AuditEvent describes a postgres host-function call for structured logging.
type AuditEvent struct {
	PluginID     string
	HostName     string
	Action       string
	SQL          string
	Readonly     bool
	DryRun       bool
	RowCount     int
	RowsAffected int64
}

// LogAudit emits a structured audit log line (SQL hash + truncated preview, never DSN/params).
func LogAudit(ev AuditEvent) {
	preview := strings.TrimSpace(ev.SQL)
	if len(preview) > 120 {
		preview = preview[:120] + "…"
	}
	sum := sha256.Sum256([]byte(ev.SQL))
	zap.L().Info("plugin postgres",
		zap.String("plugin_id", ev.PluginID),
		zap.String("host_name", ev.HostName),
		zap.String("action", ev.Action),
		zap.String("sql_sha256", hex.EncodeToString(sum[:])),
		zap.String("sql_preview", preview),
		zap.Bool("readonly", ev.Readonly),
		zap.Bool("dry_run", ev.DryRun),
		zap.Int("row_count", ev.RowCount),
		zap.Int64("rows_affected", ev.RowsAffected),
	)
}
