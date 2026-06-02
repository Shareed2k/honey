package webserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/anomaly"
)

type feedbackSuggestRequest struct {
	Line   string `json:"line"`
	Source string `json:"source"`
}

type feedbackSuggestResponse struct {
	Anomaly bool    `json:"anomaly"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
}

func (s *Server) handleLogsFeedbackSuggest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req feedbackSuggestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Line) == "" {
		httpError(w, errors.New("line is required"), http.StatusBadRequest)
		return
	}

	det := anomaly.NewLLMDetector(
		s.opts.Config.Defaults.Logs.AnomalyEndpoint,
		s.opts.Config.Defaults.Logs.AnomalyLLMModel,
		s.opts.Config.Defaults.Logs.AnomalyThresh,
		0,
	)
	res, err := det.Score(r.Context(), req.Line)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(feedbackSuggestResponse{
		Anomaly: res.Anomaly,
		Score:   res.Score,
		Reason:  strings.TrimPrefix(res.Reason, "llm:"),
	})
}
