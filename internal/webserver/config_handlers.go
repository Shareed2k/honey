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

func (s *Server) handleConfigSchema(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"json_schema": config.BuildJSONSchema(),
		"ui_schema":   config.BuildUISchema(),
	})
}

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
