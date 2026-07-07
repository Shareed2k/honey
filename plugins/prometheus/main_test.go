//go:build !wasip1 && !wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type fakeQueryAPI struct {
	value    model.Value
	warnings promv1.Warnings
	err      error
}

func (f fakeQueryAPI) Query(_ context.Context, _ string, _ time.Time, _ ...promv1.Option) (model.Value, promv1.Warnings, error) {
	return f.value, f.warnings, f.err
}

func TestValidateAction(t *testing.T) {
	tests := []struct {
		name    string
		action  string
		wantErr bool
	}{
		{name: "query is supported", action: "query", wantErr: false},
		{name: "empty is rejected", action: "", wantErr: true},
		{name: "alerts is rejected (not implemented yet)", action: "alerts", wantErr: true},
		{name: "case sensitive", action: "Query", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAction(tt.action)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateAction(%q) error = %v, wantErr %v", tt.action, err, tt.wantErr)
			}
		})
	}
}

func TestParseQueryTimeout(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    time.Duration
		wantErr bool
	}{
		{name: "empty defaults to 10s", in: "", want: 10 * time.Second},
		{name: "explicit duration", in: "5s", want: 5 * time.Second},
		{name: "invalid duration", in: "not-a-duration", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseQueryTimeout(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseQueryTimeout(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Fatalf("parseQueryTimeout(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestShapePromValue(t *testing.T) {
	tests := []struct {
		name         string
		value        model.Value
		warnings     promv1.Warnings
		wantType     string
		wantScalar   *float64
		wantWarnings []string
	}{
		{
			name: "single sample vector sets scalar",
			value: model.Vector{
				{Metric: model.Metric{"__name__": "up"}, Value: 1},
			},
			wantType:   "vector",
			wantScalar: floatPtr(1),
		},
		{
			name: "multi sample vector has no scalar",
			value: model.Vector{
				{Metric: model.Metric{"instance": "a"}, Value: 1},
				{Metric: model.Metric{"instance": "b"}, Value: 0},
			},
			wantType:   "vector",
			wantScalar: nil,
		},
		{
			name:       "scalar value",
			value:      &model.Scalar{Value: 42},
			wantType:   "scalar",
			wantScalar: floatPtr(42),
		},
		{
			name:       "string value",
			value:      &model.String{Value: "hello"},
			wantType:   "string",
			wantScalar: nil,
		},
		{
			name:         "warnings preserved alongside a valid result",
			value:        model.Vector{{Metric: model.Metric{}, Value: 1}},
			warnings:     promv1.Warnings{"partial results"},
			wantType:     "vector",
			wantScalar:   floatPtr(1),
			wantWarnings: []string{"partial results"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := shapePromValue(tt.value, tt.warnings)
			if out.Type != tt.wantType {
				t.Fatalf("Type = %q, want %q", out.Type, tt.wantType)
			}
			if (out.Scalar == nil) != (tt.wantScalar == nil) {
				t.Fatalf("Scalar = %v, want %v", out.Scalar, tt.wantScalar)
			}
			if out.Scalar != nil && tt.wantScalar != nil && *out.Scalar != *tt.wantScalar {
				t.Fatalf("Scalar = %v, want %v", *out.Scalar, *tt.wantScalar)
			}
			if tt.wantWarnings != nil && (len(out.Warnings) != len(tt.wantWarnings) || out.Warnings[0] != tt.wantWarnings[0]) {
				t.Fatalf("Warnings = %v, want %v", out.Warnings, tt.wantWarnings)
			}
		})
	}
}

func TestExecuteQuery_EmptyQueryRejected(t *testing.T) {
	api := fakeQueryAPI{err: errors.New("Query must not be called for an empty query string")}
	_, err := executeQuery(context.Background(), api, promConfig{Query: ""}, time.Now())
	if err == nil {
		t.Fatal("expected error for empty query, got nil")
	}
}

func TestExecuteQuery_InvalidTimeoutRejected(t *testing.T) {
	api := fakeQueryAPI{err: errors.New("Query must not be called for an invalid timeout")}
	_, err := executeQuery(context.Background(), api, promConfig{Query: "up", Timeout: "not-a-duration"}, time.Now())
	if err == nil {
		t.Fatal("expected error for invalid timeout, got nil")
	}
}

func TestExecuteQuery_APIErrorSurfaced(t *testing.T) {
	sentinel := errors.New("connection refused")
	api := fakeQueryAPI{err: sentinel}
	_, err := executeQuery(context.Background(), api, promConfig{Query: "up"}, time.Now())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected error chain to contain %v, got %v", sentinel, err)
	}
}

func TestExecuteQuery_SuccessProducesShapedJSON(t *testing.T) {
	api := fakeQueryAPI{value: model.Vector{
		{Metric: model.Metric{"__name__": "up"}, Value: 1},
	}}
	out, err := executeQuery(context.Background(), api, promConfig{Query: "up"}, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var decoded queryOutput
	if jsonErr := json.Unmarshal(out, &decoded); jsonErr != nil {
		t.Fatalf("decode output: %v", jsonErr)
	}
	if decoded.Type != "vector" {
		t.Fatalf("Type = %q, want vector", decoded.Type)
	}
	if decoded.Scalar == nil || *decoded.Scalar != 1 {
		t.Fatalf("Scalar = %v, want 1", decoded.Scalar)
	}
}

// capturingQueryAPI records the ts it was called with, to verify executeQuery
// passes the caller's timestamp through unchanged rather than substituting
// its own (e.g. time.Now()) — see main.go for why that distinction matters
// under a WASM guest with a fake/frozen clock.
type capturingQueryAPI struct {
	gotTS time.Time
}

func (c *capturingQueryAPI) Query(_ context.Context, _ string, ts time.Time, _ ...promv1.Option) (model.Value, promv1.Warnings, error) {
	c.gotTS = ts
	return model.Vector{}, nil, nil
}

func TestExecuteQuery_PassesThroughZeroTimestamp(t *testing.T) {
	api := &capturingQueryAPI{}
	if _, err := executeQuery(context.Background(), api, promConfig{Query: "up"}, time.Time{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !api.gotTS.IsZero() {
		t.Fatalf("Query called with ts=%v, want zero time (so Prometheus evaluates using its own clock)", api.gotTS)
	}
}

func floatPtr(f float64) *float64 { return &f }
