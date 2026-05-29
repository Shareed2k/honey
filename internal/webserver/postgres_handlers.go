package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/appsecret"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/proxy"
)

type postgresCatalogResponse struct {
	Databases []string            `json:"databases"`
	Schemas   []string            `json:"schemas"`
	Tables    map[string][]string `json:"tables"`
	Columns   map[string][]string `json:"columns"`
}

type postgresQueryRequest struct {
	SessionID string `json:"session_id"`
	SQL       string `json:"sql"`
	Database  string `json:"database,omitempty"`
	Readonly  *bool  `json:"readonly,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type postgresQueryResponse struct {
	Rows []map[string]any `json:"rows"`
}

func (s *Server) handlePostgresCatalog(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sid == "" {
		http.Error(w, `{"error":"session_id required"}`, http.StatusBadRequest)
		return
	}
	sess, err := s.getProxySessionByID(sid)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	dsn, err := postgresDSNForSession(r.Context(), s.opts.Config, sess, "")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	dbs, err := postgres.Query(ctx, s.pgPools, dsn, `SELECT datname FROM pg_database WHERE datistemplate = false ORDER BY datname`, nil, postgres.QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	sch, err := postgres.Query(ctx, s.pgPools, dsn, `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema') ORDER BY schema_name`, nil, postgres.QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	tb, err := postgres.Query(ctx, s.pgPools, dsn, `SELECT table_schema, table_name FROM information_schema.tables WHERE table_type='BASE TABLE' AND table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name`, nil, postgres.QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	col, err := postgres.Query(ctx, s.pgPools, dsn, `SELECT table_schema, table_name, column_name FROM information_schema.columns WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name, ordinal_position`, nil, postgres.QueryOpts{Timeout: 10 * time.Second, Readonly: true})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	out := postgresCatalogResponse{Tables: map[string][]string{}, Columns: map[string][]string{}}
	for _, r := range dbs.Rows {
		if v, ok := r["datname"].(string); ok {
			out.Databases = append(out.Databases, v)
		}
	}
	for _, r := range sch.Rows {
		if v, ok := r["schema_name"].(string); ok {
			out.Schemas = append(out.Schemas, v)
		}
	}
	for _, r := range tb.Rows {
		schema, sok := r["table_schema"].(string)
		table, tok := r["table_name"].(string)
		if sok && tok {
			out.Tables[schema] = append(out.Tables[schema], table)
		}
	}
	for _, r := range col.Rows {
		schema, sok := r["table_schema"].(string)
		table, tok := r["table_name"].(string)
		column, cok := r["column_name"].(string)
		if sok && tok && cok {
			key := schema + "." + table
			out.Columns[key] = append(out.Columns[key], column)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handlePostgresQuery(w http.ResponseWriter, r *http.Request) {
	var req postgresQueryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.SQL) == "" {
		http.Error(w, `{"error":"session_id and sql required"}`, http.StatusBadRequest)
		return
	}
	sess, err := s.getProxySessionByID(req.SessionID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	dsn, err := postgresDSNForSession(r.Context(), s.opts.Config, sess, req.Database)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	readonly := true
	if req.Readonly != nil {
		readonly = *req.Readonly
	}
	query := strings.TrimSpace(req.SQL)
	query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
	timeout := 15 * time.Second
	if req.TimeoutMS > 0 {
		timeout = time.Duration(req.TimeoutMS) * time.Millisecond
	}
	res, err := postgres.Query(r.Context(), s.pgPools, dsn, query, nil, postgres.QueryOpts{Timeout: timeout, Readonly: readonly})
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(postgresQueryResponse{Rows: res.Rows})
}

func (s *Server) getProxySessionByID(id string) (*proxy.Session, error) {
	sessions, err := s.proxy.List()
	if err != nil {
		return nil, err
	}
	for i := range sessions {
		if sessions[i].ID == id {
			if sessions[i].App.Type != "tcp" || !strings.EqualFold(strings.TrimSpace(sessions[i].App.Mode), "postgres") {
				return nil, fmt.Errorf("session is not postgres tcp mode")
			}
			if strings.TrimSpace(sessions[i].LocalAddr) == "" {
				return nil, fmt.Errorf("session has no local address")
			}
			return &sessions[i], nil
		}
	}
	return nil, fmt.Errorf("session %q not found", id)
}

func postgresDSNForSession(ctx context.Context, cfg *config.File, sess *proxy.Session, dbOverride string) (string, error) {
	upstreamRaw := strings.TrimSpace(sess.App.Upstream)
	if cfg != nil {
		if dec, err := appsecret.ResolveUpstream(ctx, cfg, upstreamRaw); err == nil {
			upstreamRaw = dec
		}
	}
	u, err := url.Parse(upstreamRaw)
	if err != nil {
		return "", fmt.Errorf("parse app upstream: %w", err)
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", fmt.Errorf("upstream must be postgres:// or postgresql://")
	}
	host, port, err := net.SplitHostPort(strings.TrimSpace(sess.LocalAddr))
	if err != nil {
		return "", fmt.Errorf("invalid local address: %w", err)
	}
	u.Host = net.JoinHostPort(host, port)
	if strings.TrimSpace(dbOverride) != "" {
		u.Path = "/" + strings.TrimPrefix(strings.TrimSpace(dbOverride), "/")
	}
	q := u.Query()
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "disable")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
