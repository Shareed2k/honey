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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "path": cfgPath})
}

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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "path": cfgPath})
}

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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok", "path": cfgPath})
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
