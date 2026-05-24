// Package metrics provides Prometheus instrumentation for honey web.
package metrics

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds honey Prometheus collectors on a dedicated registry.
type Registry struct {
	reg *prometheus.Registry

	buildInfo      *prometheus.GaugeVec
	httpRequests   *prometheus.CounterVec
	httpDuration   *prometheus.HistogramVec
	searchRequests *prometheus.CounterVec
	searchDuration prometheus.Histogram
	searchRecords  prometheus.Histogram
	wsActive       *prometheus.GaugeVec

	recipeRuns              *prometheus.CounterVec
	recipeRunDuration       *prometheus.HistogramVec
	recipeSteps             *prometheus.CounterVec
	recipeStepDuration      *prometheus.HistogramVec
	recipeStepRetryAttempts *prometheus.CounterVec
	recipeHostResults       *prometheus.CounterVec
	pluginExecutions        *prometheus.CounterVec
	pluginExecutionDuration *prometheus.HistogramVec
	sshOperations           *prometheus.CounterVec
	sshOperationDuration    *prometheus.HistogramVec
	agentTransfers          *prometheus.CounterVec
	agentTransferDuration   prometheus.Histogram
	recipeValidate          *prometheus.CounterVec
	recipeValidateDuration  prometheus.Histogram
	execCommands            *prometheus.CounterVec
	execCommandDuration     prometheus.Histogram
	execCommandHosts        prometheus.Histogram
}

// NewRegistry registers honey and standard process/Go collectors.
func NewRegistry(version, commit string) *Registry {
	reg := prometheus.NewRegistry()
	r := &Registry{
		reg: reg,
		buildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "honey_build_info",
			Help: "Build version and commit (value is always 1).",
		}, []string{"version", "commit"}),
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_http_requests_total",
			Help: "Total HTTP requests served by honey web.",
		}, []string{"method", "route", "code"}),
		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "honey_http_request_duration_seconds",
			Help:    "HTTP request latency for honey web.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		searchRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_search_requests_total",
			Help: "Total host search API requests.",
		}, []string{"status"}),
		searchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "honey_search_duration_seconds",
			Help:    "Host search API latency.",
			Buckets: prometheus.DefBuckets,
		}),
		searchRecords: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "honey_search_records",
			Help:    "Number of records returned by a successful search.",
			Buckets: []float64{0, 1, 5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
		}),
		wsActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "honey_ws_connections_active",
			Help: "Active WebSocket terminal sessions.",
		}, []string{"kind"}),
		recipeRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_recipe_runs_total",
			Help: "Total CUE recipe dry-runs and executions.",
		}, []string{"mode", "type", "status"}),
		recipeRunDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "honey_recipe_run_duration_seconds",
			Help:    "CUE recipe dry-run and execute latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"mode", "type"}),
		recipeSteps: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_recipe_steps_total",
			Help: "Total CUE recipe steps completed.",
		}, []string{"kind", "status"}),
		recipeStepDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "honey_recipe_step_duration_seconds",
			Help:    "CUE recipe step latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"kind"}),
		recipeStepRetryAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_recipe_step_retry_attempts_total",
			Help: "Extra step retry attempts after the first try.",
		}, []string{"kind"}),
		recipeHostResults: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_recipe_host_results_total",
			Help: "Per-host rows emitted during recipe execution.",
		}, []string{"status"}),
		pluginExecutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_plugin_executions_total",
			Help: "WASM plugin step executions.",
		}, []string{"plugin_id", "action", "status"}),
		pluginExecutionDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "honey_plugin_execution_duration_seconds",
			Help:    "WASM plugin step latency (final attempt).",
			Buckets: prometheus.DefBuckets,
		}, []string{"plugin_id", "action"}),
		sshOperations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_ssh_operations_total",
			Help: "SSH, SFTP, script, and TrueNAS remote operations.",
		}, []string{"operation", "status"}),
		sshOperationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "honey_ssh_operation_duration_seconds",
			Help:    "SSH, SFTP, script, and TrueNAS operation latency (final attempt).",
			Buckets: prometheus.DefBuckets,
		}, []string{"operation"}),
		agentTransfers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_agent_transfers_total",
			Help: "Agent-based cloud file transfers.",
		}, []string{"status"}),
		agentTransferDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "honey_agent_transfer_duration_seconds",
			Help:    "Agent-based cloud file transfer latency.",
			Buckets: prometheus.DefBuckets,
		}),
		recipeValidate: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_recipe_validate_total",
			Help: "Recipe validate-content API calls.",
		}, []string{"status"}),
		recipeValidateDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "honey_recipe_validate_duration_seconds",
			Help:    "Recipe validate-content API latency.",
			Buckets: prometheus.DefBuckets,
		}),
		execCommands: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "honey_exec_commands_total",
			Help: "Raw exec API command batches.",
		}, []string{"status"}),
		execCommandDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "honey_exec_command_duration_seconds",
			Help:    "Raw exec API batch latency.",
			Buckets: prometheus.DefBuckets,
		}),
		execCommandHosts: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "honey_exec_command_hosts",
			Help:    "Host count per raw exec API batch.",
			Buckets: []float64{0, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000},
		}),
	}
	reg.MustRegister(
		r.buildInfo,
		r.httpRequests,
		r.httpDuration,
		r.searchRequests,
		r.searchDuration,
		r.searchRecords,
		r.wsActive,
		r.recipeRuns,
		r.recipeRunDuration,
		r.recipeSteps,
		r.recipeStepDuration,
		r.recipeStepRetryAttempts,
		r.recipeHostResults,
		r.pluginExecutions,
		r.pluginExecutionDuration,
		r.sshOperations,
		r.sshOperationDuration,
		r.agentTransfers,
		r.agentTransferDuration,
		r.recipeValidate,
		r.recipeValidateDuration,
		r.execCommands,
		r.execCommandDuration,
		r.execCommandHosts,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	if version == "" {
		version = "unknown"
	}
	if commit == "" {
		commit = "unknown"
	}
	r.buildInfo.WithLabelValues(version, commit).Set(1)
	return r
}

// Handler exposes Prometheus metrics for scraping.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}

// Middleware records request counts and latency for honey web.
func (r *Registry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, req)
		route := req.Pattern
		if route == "" {
			route = "unknown"
		}
		code := strconv.Itoa(rw.status)
		r.httpRequests.WithLabelValues(req.Method, route, code).Inc()
		r.httpDuration.WithLabelValues(req.Method, route).Observe(time.Since(start).Seconds())
	})
}

// ObserveSearch records search handler outcome and timing.
func (r *Registry) ObserveSearch(err error, duration time.Duration, recordCount int) {
	status := "ok"
	if err != nil {
		status = "error"
	}
	r.searchRequests.WithLabelValues(status).Inc()
	r.searchDuration.Observe(duration.Seconds())
	if err == nil {
		r.searchRecords.Observe(float64(recordCount))
	}
}

// IncWS increments active WebSocket sessions for kind.
func (r *Registry) IncWS(kind string) {
	r.wsActive.WithLabelValues(kind).Inc()
}

// DecWS decrements active WebSocket sessions for kind.
func (r *Registry) DecWS(kind string) {
	r.wsActive.WithLabelValues(kind).Dec()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Hijack forwards to the underlying ResponseWriter so WebSocket upgrades still work
// when honey web runs with --metrics-listen (this middleware wraps all handlers).
func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("metrics statusRecorder: underlying ResponseWriter is not an http.Hijacker")
	}
	return h.Hijack()
}

// Flush forwards to the underlying ResponseWriter when supported (SSE/streaming).
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
