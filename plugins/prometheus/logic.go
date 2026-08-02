// Package main implements the Honey prometheus WASM plugin.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	actionQuery         = "query"
	defaultQueryTimeout = 10 * time.Second
)

// queryAPI is the narrow slice of promv1.API this plugin needs. The real
// promv1.API satisfies it automatically; tests fake only Query.
type queryAPI interface {
	Query(ctx context.Context, query string, ts time.Time, opts ...promv1.Option) (model.Value, promv1.Warnings, error)
}

// validateAction rejects any action other than "query" — future actions
// (alerts, query_range, suggest) add explicit branches here, not a silent
// fallback.
func validateAction(action string) error {
	if action != actionQuery {
		return fmt.Errorf("unsupported action %q, only %q is supported", action, actionQuery)
	}
	return nil
}

// parseQueryTimeout parses cfg.Timeout, defaulting to defaultQueryTimeout when empty.
func parseQueryTimeout(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return defaultQueryTimeout, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout %q: %w", s, err)
	}
	return d, nil
}

// executeQuery runs a PromQL instant query and returns the shaped result as
// JSON, or an error if the query is invalid, the timeout is malformed, or the
// query itself fails.
func executeQuery(ctx context.Context, api queryAPI, cfg promConfig, now time.Time) ([]byte, error) {
	if strings.TrimSpace(cfg.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	timeout, err := parseQueryTimeout(cfg.Timeout)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	value, warnings, err := api.Query(ctx, cfg.Query, now)
	if err != nil {
		return nil, fmt.Errorf("query prometheus: %w", err)
	}
	out := shapePromValue(value, warnings)
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal result: %w", err)
	}
	return b, nil
}

// shapePromValue converts a Prometheus query result into the plugin's stable
// output shape. Warnings are preserved regardless of the result type — a
// successful query can still carry warnings (e.g. partial results).
func shapePromValue(value model.Value, warnings promv1.Warnings) queryOutput {
	out := queryOutput{
		Type: value.Type().String(),
	}
	if len(warnings) > 0 {
		out.Warnings = append([]string(nil), warnings...)
	}
	switch v := value.(type) {
	case model.Vector:
		rows := make([]map[string]any, 0, len(v))
		for _, sample := range v {
			rows = append(rows, map[string]any{
				"metric": sample.Metric,
				"value":  float64(sample.Value),
			})
		}
		out.Result = rows
		if len(v) == 1 {
			scalar := float64(v[0].Value)
			out.Scalar = &scalar
		}
	case *model.Scalar:
		scalar := float64(v.Value)
		out.Result = scalar
		out.Scalar = &scalar
	case *model.String:
		out.Result = v.Value
	default:
		out.Result = value.String()
	}
	return out
}
