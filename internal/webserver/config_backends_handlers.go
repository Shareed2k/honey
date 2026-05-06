package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/shareed2k/honey/internal/config"
)

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
	switch kind {
	case "gcp":
		var b config.GCPBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode gcp backend: %w", err)
		}
		cfg.Backends.GCP = append(cfg.Backends.GCP, b)
	case "aws":
		var b config.AWSBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode aws backend: %w", err)
		}
		cfg.Backends.AWS = append(cfg.Backends.AWS, b)
	case "kubernetes":
		var b config.KubernetesBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode kubernetes backend: %w", err)
		}
		cfg.Backends.Kubernetes = append(cfg.Backends.Kubernetes, b)
	case "consul":
		var b config.ConsulBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode consul backend: %w", err)
		}
		cfg.Backends.Consul = append(cfg.Backends.Consul, b)
	case "proxmox":
		var b config.ProxmoxBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode proxmox backend: %w", err)
		}
		cfg.Backends.Proxmox = append(cfg.Backends.Proxmox, b)
	default:
		return fmt.Errorf("unknown backend kind %q (use gcp, aws, kubernetes, consul, proxmox)", kind)
	}
	return nil
}

func replaceBackendByKind(cfg *config.File, kind string, idx int, body []byte) error {
	switch kind {
	case "gcp":
		if idx >= len(cfg.Backends.GCP) {
			return fmt.Errorf("index %d out of range for gcp (len=%d)", idx, len(cfg.Backends.GCP))
		}
		var b config.GCPBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode gcp backend: %w", err)
		}
		cfg.Backends.GCP[idx] = b
	case "aws":
		if idx >= len(cfg.Backends.AWS) {
			return fmt.Errorf("index %d out of range for aws (len=%d)", idx, len(cfg.Backends.AWS))
		}
		var b config.AWSBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode aws backend: %w", err)
		}
		cfg.Backends.AWS[idx] = b
	case "kubernetes":
		if idx >= len(cfg.Backends.Kubernetes) {
			return fmt.Errorf("index %d out of range for kubernetes (len=%d)", idx, len(cfg.Backends.Kubernetes))
		}
		var b config.KubernetesBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode kubernetes backend: %w", err)
		}
		cfg.Backends.Kubernetes[idx] = b
	case "consul":
		if idx >= len(cfg.Backends.Consul) {
			return fmt.Errorf("index %d out of range for consul (len=%d)", idx, len(cfg.Backends.Consul))
		}
		var b config.ConsulBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode consul backend: %w", err)
		}
		cfg.Backends.Consul[idx] = b
	case "proxmox":
		if idx >= len(cfg.Backends.Proxmox) {
			return fmt.Errorf("index %d out of range for proxmox (len=%d)", idx, len(cfg.Backends.Proxmox))
		}
		var b config.ProxmoxBackend
		if err := json.Unmarshal(body, &b); err != nil {
			return fmt.Errorf("decode proxmox backend: %w", err)
		}
		cfg.Backends.Proxmox[idx] = b
	default:
		return fmt.Errorf("unknown backend kind %q (use gcp, aws, kubernetes, consul, proxmox)", kind)
	}
	return nil
}

func deleteBackendByKind(cfg *config.File, kind string, idx int) error {
	switch kind {
	case "gcp":
		if idx < 0 || idx >= len(cfg.Backends.GCP) {
			return fmt.Errorf("index %d out of range for gcp (len=%d)", idx, len(cfg.Backends.GCP))
		}
		cfg.Backends.GCP = append(cfg.Backends.GCP[:idx], cfg.Backends.GCP[idx+1:]...)
	case "aws":
		if idx < 0 || idx >= len(cfg.Backends.AWS) {
			return fmt.Errorf("index %d out of range for aws (len=%d)", idx, len(cfg.Backends.AWS))
		}
		cfg.Backends.AWS = append(cfg.Backends.AWS[:idx], cfg.Backends.AWS[idx+1:]...)
	case "kubernetes":
		if idx < 0 || idx >= len(cfg.Backends.Kubernetes) {
			return fmt.Errorf("index %d out of range for kubernetes (len=%d)", idx, len(cfg.Backends.Kubernetes))
		}
		cfg.Backends.Kubernetes = append(cfg.Backends.Kubernetes[:idx], cfg.Backends.Kubernetes[idx+1:]...)
	case "consul":
		if idx < 0 || idx >= len(cfg.Backends.Consul) {
			return fmt.Errorf("index %d out of range for consul (len=%d)", idx, len(cfg.Backends.Consul))
		}
		cfg.Backends.Consul = append(cfg.Backends.Consul[:idx], cfg.Backends.Consul[idx+1:]...)
	case "proxmox":
		if idx < 0 || idx >= len(cfg.Backends.Proxmox) {
			return fmt.Errorf("index %d out of range for proxmox (len=%d)", idx, len(cfg.Backends.Proxmox))
		}
		cfg.Backends.Proxmox = append(cfg.Backends.Proxmox[:idx], cfg.Backends.Proxmox[idx+1:]...)
	default:
		return fmt.Errorf("unknown backend kind %q (use gcp, aws, kubernetes, consul, proxmox)", kind)
	}
	return nil
}
