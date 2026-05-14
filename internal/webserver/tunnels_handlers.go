package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// limit size to prevent OOM
	if b.buf.Len() > 1024*1024 {
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type activeTunnel struct {
	ID        string    `json:"id"`
	Host      string    `json:"host"`
	RecordKey string    `json:"record_key"`
	Mapping   string    `json:"mapping"`
	StartedAt time.Time `json:"started_at"`
	Error     string    `json:"error,omitempty"`
	cancel    context.CancelFunc
	logBuf    *safeBuffer
}

type tunnelManager struct {
	mu      sync.Mutex
	tunnels map[string]*activeTunnel
}

func newTunnelManager() *tunnelManager {
	return &tunnelManager{
		tunnels: make(map[string]*activeTunnel),
	}
}

func (m *tunnelManager) list() []activeTunnel {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]activeTunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		res = append(res, *t)
	}
	return res
}

func (m *tunnelManager) start(user string, r hosts.Record, mapping string) *activeTunnel {
	id := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	hostName := r.Name
	if hostName == "" {
		hostName = r.PrimaryIP
	}

	recordKey := fmt.Sprintf("%s\x1e%s\x1e%s", r.Provider, r.Name, r.PrimaryIP)

	logBuf := &safeBuffer{}

	t := &activeTunnel{
		ID:        id,
		Host:      hostName,
		RecordKey: recordKey,
		Mapping:   mapping,
		StartedAt: time.Now().UTC(),
		cancel:    cancel,
		logBuf:    logBuf,
	}

	m.mu.Lock()
	m.tunnels[id] = t
	m.mu.Unlock()

	zap.L().Debug("starting background tunnel", zap.String("id", id), zap.String("host", hostName), zap.String("mapping", mapping))

	go func() {
		executor := ui.GetExecutor(r)
		err := executor.RunTunnel(ctx, user, r, mapping, logBuf)

		m.mu.Lock()
		defer m.mu.Unlock()
		if existing, ok := m.tunnels[id]; ok {
			switch {
			case err != nil && ctx.Err() == nil:
				zap.L().Debug("tunnel closed with error", zap.String("id", id), zap.Error(err))
				existing.Error = err.Error()
			case ctx.Err() == nil:
				zap.L().Debug("tunnel closed unexpectedly without error", zap.String("id", id))
				// Tunnel stopped unexpectedly without error
				existing.Error = "Tunnel closed unexpectedly"
			default:
				zap.L().Debug("tunnel cancelled", zap.String("id", id))
				// Tunnel was cancelled, remove it
				delete(m.tunnels, id)
			}
		} else {
			zap.L().Debug("tunnel closed but no longer tracked", zap.String("id", id), zap.Error(err))
		}
	}()

	return t
}

func (m *tunnelManager) stop(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	t, ok := m.tunnels[id]
	if !ok {
		zap.L().Debug("stop tunnel failed: not found", zap.String("id", id))
		return fmt.Errorf("tunnel not found")
	}

	zap.L().Debug("stopping tunnel", zap.String("id", id), zap.String("host", t.Host))
	t.cancel()
	delete(m.tunnels, id)
	return nil
}

func (s *Server) handleTunnelsLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httpError(w, fmt.Errorf("missing tunnel id"), http.StatusBadRequest)
		return
	}

	s.tunnels.mu.Lock()
	t, ok := s.tunnels.tunnels[id]
	s.tunnels.mu.Unlock()

	if !ok {
		httpError(w, fmt.Errorf("tunnel not found"), http.StatusNotFound)
		return
	}

	logs := ""
	if t.logBuf != nil {
		logs = t.logBuf.String()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"logs": logs})
}

func (s *Server) handleTunnelsGet(w http.ResponseWriter, _ *http.Request) {
	list := s.tunnels.list()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tunnels": list})
}

type startTunnelReq struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Mapping string       `json:"mapping"`
}

func (s *Server) handleTunnelsPost(w http.ResponseWriter, r *http.Request) {
	var req startTunnelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("invalid json"), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Mapping) == "" {
		httpError(w, fmt.Errorf("mapping is required"), http.StatusBadRequest)
		return
	}

	user := strings.TrimSpace(req.SSHUser)
	tunnel := s.tunnels.start(user, req.Record, req.Mapping)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"tunnel": tunnel})
}

func (s *Server) handleTunnelsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		httpError(w, fmt.Errorf("missing tunnel id"), http.StatusBadRequest)
		return
	}

	if err := s.tunnels.stop(id); err != nil {
		httpError(w, err, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
}
