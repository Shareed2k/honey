package webserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/config"
)

// handleLogsDefault returns logs defaults from honey config.
// @Summary Logs defaults
// @Tags logs
// @Produce json
// @Success 200 {object} config.Logs
// @Failure 400 {object} map[string]string
// @Router /api/v1/logs/default [get]
// @Security BearerAuth
func (s *Server) handleLogsDefault(w http.ResponseWriter, _ *http.Request) {
	cfgPath, err := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cfg.Defaults.Logs)
}
