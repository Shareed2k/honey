package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"
)

func decodeBackendElement(body []byte, slice reflect.Value, kind string) (reflect.Value, error) {
	elemPtr := reflect.New(slice.Type().Elem())
	if err := json.Unmarshal(body, elemPtr.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("decode %s backend: %w", kind, err)
	}
	return elemPtr.Elem(), nil
}

func (s *Server) resolveWritableConfigPath() (string, error) {
	cfgPath, err := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	if err != nil {
		return "", err
	}
	if cfgPath == "" {
		return "", fmt.Errorf("no config file resolved")
	}
	return cfgPath, nil
}

// handleConfigBackendsGet returns the backends section of the config file.
// @Summary List backends from config
// @Tags config
// @Produce json
// @Success 200 {object} config.Backends
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/config/backends [get]
// @Security BearerAuth
func (s *Server) handleConfigBackendsGet(w http.ResponseWriter, r *http.Request) {
	_ = r
	cfgPath, err := s.resolveWritableConfigPath()
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Config-Path", cfgPath)
	_ = json.NewEncoder(w).Encode(cfg.Backends)
}

// handleConfigBackendsPost appends a backend entry for the given kind.
// @Summary Append backend entry
// @Tags config
// @Accept json
// @Produce json
// @Param kind path string true "backend kind (gcp, aws, kubernetes, consul, proxmox, truenas, local, docker)"
// @Param body body ConfigBackendEntryBody true "Backend entry JSON (shape depends on kind path param)"
// @Success 200 {object} StatusResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/config/backends/{kind} [post]
// @Security BearerAuth
func (s *Server) handleConfigBackendsPost(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.PathValue("kind")))
	cfgPath, err := s.resolveWritableConfigPath()
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := appendBackendByKind(cfg, kind, body); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := cfg.Validate(); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := saveConfigFile(cfgPath, cfg); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	s.applyInMemoryConfig(cfgPath, cfg)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(StatusResponse{Status: "ok", Path: cfgPath})
}

// handleConfigBackendsPut replaces a backend entry by index.
// @Summary Replace backend entry
// @Tags config
// @Accept json
// @Produce json
// @Param kind path string true "backend kind"
// @Param index path int true "0-based index in that kind's list"
// @Param body body ConfigBackendEntryBody true "Backend entry JSON (shape depends on kind path param)"
// @Success 200 {object} StatusResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/config/backends/{kind}/{index} [put]
// @Security BearerAuth
func (s *Server) handleConfigBackendsPut(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.PathValue("kind")))
	idxStr := strings.TrimSpace(r.PathValue("index"))
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 {
		httpError(w, fmt.Errorf("invalid index %q", idxStr), http.StatusBadRequest)
		return
	}
	cfgPath, err := s.resolveWritableConfigPath()
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := replaceBackendByKind(cfg, kind, idx, body); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := cfg.Validate(); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := saveConfigFile(cfgPath, cfg); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	s.applyInMemoryConfig(cfgPath, cfg)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(StatusResponse{Status: "ok", Path: cfgPath})
}

// handleConfigBackendsDelete removes a backend entry by index.
// @Summary Delete backend entry
// @Tags config
// @Produce json
// @Param kind path string true "backend kind"
// @Param index path int true "0-based index"
// @Success 200 {object} StatusResponse
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/config/backends/{kind}/{index} [delete]
// @Security BearerAuth
func (s *Server) handleConfigBackendsDelete(w http.ResponseWriter, r *http.Request) {
	kind := strings.ToLower(strings.TrimSpace(r.PathValue("kind")))
	idxStr := strings.TrimSpace(r.PathValue("index"))
	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 0 {
		httpError(w, fmt.Errorf("invalid index %q", idxStr), http.StatusBadRequest)
		return
	}
	cfgPath, err := s.resolveWritableConfigPath()
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if err := deleteBackendByKind(cfg, kind, idx); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := saveConfigFile(cfgPath, cfg); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	s.applyInMemoryConfig(cfgPath, cfg)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(StatusResponse{Status: "ok", Path: cfgPath})
}

func appendBackendByKind(cfg *config.File, kind string, body []byte) error {
	slice, err := searchrun.GetBackendSliceByKind(cfg, kind)
	if err != nil {
		return err
	}
	elem, err := decodeBackendElement(body, slice, kind)
	if err != nil {
		return err
	}
	slice.Set(reflect.Append(slice, elem))
	return nil
}

func replaceBackendByKind(cfg *config.File, kind string, idx int, body []byte) error {
	slice, err := searchrun.GetBackendSliceByKind(cfg, kind)
	if err != nil {
		return err
	}
	if idx >= slice.Len() {
		return fmt.Errorf("index %d out of range for %s (len=%d)", idx, kind, slice.Len())
	}
	elem, err := decodeBackendElement(body, slice, kind)
	if err != nil {
		return err
	}
	slice.Index(idx).Set(elem)
	return nil
}

func deleteBackendByKind(cfg *config.File, kind string, idx int) error {
	slice, err := searchrun.GetBackendSliceByKind(cfg, kind)
	if err != nil {
		return err
	}
	if idx < 0 || idx >= slice.Len() {
		return fmt.Errorf("index %d out of range for %s (len=%d)", idx, kind, slice.Len())
	}
	slice.Set(reflect.AppendSlice(slice.Slice(0, idx), slice.Slice(idx+1, slice.Len())))
	return nil
}
