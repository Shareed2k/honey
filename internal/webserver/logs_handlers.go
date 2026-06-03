package webserver

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/jsonutil"
	"github.com/shareed2k/honey/internal/ui"
)

type logsStreamRequest struct {
	Records               []hosts.Record `json:"records"`
	SSHUser               string         `json:"ssh_user"`
	Source                string         `json:"source"`
	Follow                bool           `json:"follow"`
	Tail                  int64          `json:"tail"`
	Since                 string         `json:"since"`
	Container             string         `json:"container"`
	Unit                  string         `json:"unit"`
	Command               string         `json:"command"`
	RunAs                 string         `json:"run_as"`
	Grep                  string         `json:"grep"`
	Labels                []string       `json:"labels"`
	Anomaly               bool           `json:"anomaly"`
	AnomalyThreshold      float64        `json:"anomaly_threshold"`
	AnomalyOnly           bool           `json:"anomaly_only"`
	AnomalyModel          string         `json:"anomaly_model"`
	AnomalyTokPath        string         `json:"anomaly_tokenizer"`
	AnomalyEndpoint       string         `json:"anomaly_endpoint"`
	AnomalyLLMModel       string         `json:"anomaly_llm_model"`
	AnomalyContextLines   int            `json:"anomaly_context"`
	AnomalyFilterThresh   float64        `json:"anomaly_filter_threshold"`
	AnomalyFreqWindow     int            `json:"anomaly_freq_window"`
	AnomalyFreqRatio      float64        `json:"anomaly_freq_ratio"`
	AlertEnabled          bool           `json:"alert_enabled"`
	AlertSuppressDuration string         `json:"alert_suppress_duration"`
	AnomalyPreprocessor   string         `json:"anomaly_preprocessor"`
}

func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	var req logsStreamRequest
	if err := jsonutil.Unmarshal(body, &req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if len(req.Records) == 0 {
		httpError(w, fmt.Errorf("no records"), http.StatusBadRequest)
		return
	}

	user := s.sshUser(req.SSHUser)

	if req.Command != "" && !s.opts.AllowLogsCommand {
		httpError(w, fmt.Errorf("command field is not allowed; start honey web with --allow-logs-command to enable"), http.StatusForbidden)
		return
	}

	zap.L().Debug("logs stream request",
		zap.Int("records", len(req.Records)),
		zap.String("user", user),
		zap.Bool("follow", req.Follow),
		zap.Int64("tail", req.Tail),
		zap.String("since", req.Since),
		zap.String("source", req.Source),
		zap.String("grep", req.Grep),
		zap.Bool("anomaly", req.Anomaly),
	)

	var since time.Duration
	if req.Since != "" {
		since, _ = time.ParseDuration(req.Since)
	}
	threshold := req.AnomalyThreshold
	if threshold <= 0 {
		threshold = 0.90
	}
	tail := req.Tail
	if tail <= 0 {
		tail = 100
	}
	freqWindow := req.AnomalyFreqWindow
	if freqWindow == 0 {
		freqWindow = 100
	}
	freqRatio := req.AnomalyFreqRatio
	if freqRatio <= 0 {
		freqRatio = 5.0
	}

	opts := ui.LogOptions{
		Source:                 req.Source,
		Follow:                 req.Follow,
		Tail:                   tail,
		Since:                  since,
		Container:              req.Container,
		Unit:                   req.Unit,
		Command:                req.Command,
		RunAs:                  req.RunAs,
		Grep:                   req.Grep,
		Labels:                 req.Labels,
		Highlight:              false,
		Anomaly:                req.Anomaly,
		AnomalyModel:           req.AnomalyModel,
		AnomalyThresh:          threshold,
		AnomalyOnly:            req.AnomalyOnly,
		AnomalyTokPath:         req.AnomalyTokPath,
		AnomalyEndpoint:        req.AnomalyEndpoint,
		AnomalyLLMModel:        req.AnomalyLLMModel,
		AnomalyContextLines:    req.AnomalyContextLines,
		AnomalyFilterThreshold: req.AnomalyFilterThresh,
		AnomalyFreqWindow:      freqWindow,
		AnomalyFreqRatio:       freqRatio,
		AnomalyPreprocessor:    req.AnomalyPreprocessor,
		AlertEnabled:           req.AlertEnabled,
		AlertSuppressDuration: func() time.Duration {
			d, _ := time.ParseDuration(req.AlertSuppressDuration)
			return d
		}(),
	}

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	fl, _ := w.(http.Flusher)
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		defer pw.Close()
		if err := ui.StreamLogs(r.Context(), user, req.Records, opts, s.fileClientCache, pw); err != nil {
			zap.L().Debug("logs stream error", zap.Error(err))
		}
	}()

	enc := jsonutil.NewEncoder(w)
	scanner := bufio.NewScanner(pr)
	lineCount := 0
	for scanner.Scan() {
		lineCount++
		_ = enc.Encode(map[string]string{"line": scanner.Text()})
		if fl != nil {
			fl.Flush()
		}
	}
	zap.L().Debug("logs stream done", zap.Int("lines", lineCount))
}
