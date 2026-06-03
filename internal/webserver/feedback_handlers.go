package webserver

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/jsonutil"
	"github.com/shareed2k/honey/internal/safepath"
)

type feedbackWebRecord struct {
	Ts      string  `json:"ts"`
	Source  string  `json:"source"`
	Line    string  `json:"line"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
	Anomaly bool    `json:"anomaly"`
}

type feedbackSaveRequest struct {
	Records []feedbackWebRecord `json:"records"`
}

func (s *Server) handleLogsFeedbackGet(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Config == nil {
		httpError(w, errors.New("no anomaly feedback file is configured"), http.StatusNotFound)
		return
	}
	filePath := s.opts.Config.Defaults.Logs.AnomalyFeedbackFile
	if filePath == "" {
		httpError(w, errors.New("no anomaly feedback file is configured"), http.StatusNotFound)
		return
	}

	data, err := safepath.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Return empty array if file doesn't exist yet
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"records":[]}`))
			return
		}
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	var records []feedbackWebRecord
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec feedbackWebRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		records = append(records, rec)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"records": records})
}

func (s *Server) handleLogsFeedbackSave(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		httpError(w, errors.New("no anomaly feedback file is configured"), http.StatusNotFound)
		return
	}
	filePath := s.opts.Config.Defaults.Logs.AnomalyFeedbackFile
	if filePath == "" {
		httpError(w, errors.New("no anomaly feedback file is configured"), http.StatusNotFound)
		return
	}

	var req feedbackSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	var buf bytes.Buffer
	for _, rec := range req.Records {
		lineBytes, err := jsonutil.Marshal(rec)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		buf.Write(lineBytes)
		buf.WriteByte('\n')
	}

	if err := safepath.WriteFile(filePath, buf.Bytes(), 0o600); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"success":true}`))
}
