package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/safepath"
)

// handleConfigSchema returns JSON Schema and UI schema for the config editor.
// @Summary Config JSON Schema
// @Tags config
// @Produce json
// @Success 200 {object} map[string]interface{} "json_schema, ui_schema"
// @Router /api/v1/config/schema [get]
// @Security BearerAuth
func (s *Server) handleConfigSchema(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"json_schema": config.BuildJSONSchema(),
		"ui_schema":   config.BuildUISchema(),
	})
}

// handleConfigGet returns the raw honey YAML.
// @Summary Get honey YAML config
// @Tags config
// @Produce application/yaml
// @Success 200 {string} string "YAML document"
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/config [get]
// @Security BearerAuth
func (s *Server) handleConfigGet(w http.ResponseWriter, _ *http.Request) {
	cfgPath, err := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if cfgPath == "" {
		httpError(w, fmt.Errorf("no config file resolved"), http.StatusNotFound)
		return
	}
	b, err := safepath.ReadFile(cfgPath)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("X-Config-Path", cfgPath)
	_, _ = w.Write(b)
}

// handleConfigPut replaces the honey YAML after validation.
// @Summary Replace honey YAML config
// @Tags config
// @Produce json
// @Param body body string true "Full YAML document (Content-Type application/yaml or text/plain)"
// @Success 200 {object} map[string]string "status, path"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/config [put]
// @Security BearerAuth
func (s *Server) handleConfigPut(w http.ResponseWriter, r *http.Request) {
	cfgPath, err := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if cfgPath == "" {
		httpError(w, fmt.Errorf("no config file resolved for PUT"), http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if _, err := config.ParseYAML(body); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	_ = backupConfigIfExists(cfgPath)
	if err := safepath.WriteFile(cfgPath, body, 0o600); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if cfg, lerr := config.Load(cfgPath); lerr == nil {
		hostexec.ReconfigureFromHoneyConfig(cfg)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "path": cfgPath})
}
