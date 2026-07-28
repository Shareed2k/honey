package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

type safeBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
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

// ActiveTunnel is one in-process SSH port-forward tunnel.
type ActiveTunnel struct {
	ID        string             `json:"id"`
	Host      string             `json:"host"`
	RecordKey string             `json:"record_key"`
	Mapping   string             `json:"mapping"`
	StartedAt time.Time          `json:"started_at"`
	Error     string             `json:"error,omitempty"`
	cancel    context.CancelFunc `json:"-"`
	logBuf    *safeBuffer        `json:"-"`
}

// TunnelsListResponse is returned by GET /api/v1/tunnels.
type TunnelsListResponse struct {
	Tunnels []ActiveTunnel `json:"tunnels"`
}

// TunnelLogsResponse is returned by GET /api/v1/tunnels/{id}/logs.
type TunnelLogsResponse struct {
	Logs string `json:"logs"`
}

// StartTunnelRequest is the JSON body for POST /api/v1/tunnels.
type StartTunnelRequest struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Mapping string       `json:"mapping"`
}

// TunnelStartResponse is returned by POST /api/v1/tunnels.
type TunnelStartResponse struct {
	Tunnel ActiveTunnel `json:"tunnel"`
}

// TunnelDeleteResponse is returned by DELETE /api/v1/tunnels/{id}.
type TunnelDeleteResponse struct {
	Success bool `json:"success"`
}

type tunnelManager struct {
	mu      sync.Mutex
	tunnels map[string]*ActiveTunnel
}

func newTunnelManager() *tunnelManager {
	return &tunnelManager{
		tunnels: make(map[string]*ActiveTunnel),
	}
}

func (m *tunnelManager) list() []ActiveTunnel {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]ActiveTunnel, 0, len(m.tunnels))
	for _, t := range m.tunnels {
		res = append(res, *t)
	}
	return res
}

func (m *tunnelManager) start(user string, r hosts.Record, mapping string, reg hostexec.Registry) *ActiveTunnel {
	id := uuid.New().String()
	ctx, cancel := context.WithCancel(context.Background())

	hostName := r.Name
	if hostName == "" {
		hostName = r.PrimaryIP
	}

	recordKey := fmt.Sprintf("%s\x1e%s\x1e%s", r.Provider, r.Name, r.PrimaryIP)
	logBuf := &safeBuffer{}

	t := &ActiveTunnel{
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
		executor := reg.ForRecord(r)
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
				existing.Error = "Tunnel closed unexpectedly"
			default:
				zap.L().Debug("tunnel cancelled", zap.String("id", id))
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

// TunnelsAPI owns the in-process SSH -L port-forward endpoints (list, logs,
// start, stop), isolating them from the main Server so the tunnels feature
// carries its own deps (mirrors FilesAPI/PostgresAPI/EnrollAPI, architecture
// candidate arch-08). It drives a tunnelManager and resolves records through the
// executor registry; sshUser is injected (Server.sshUser is shared Server-wide).
type TunnelsAPI struct {
	opts    Options
	tunnels *tunnelManager
	sshUser func(string) string
}

// NewTunnelsAPI wires the tunnel manager and the shared ssh-user resolver.
func NewTunnelsAPI(opts Options, tunnels *tunnelManager, sshUser func(string) string) *TunnelsAPI {
	return &TunnelsAPI{opts: opts, tunnels: tunnels, sshUser: sshUser}
}

// handleTunnelsLogs returns buffered log text for an SSH -L tunnel.
// @Summary Tunnel logs
// @Tags tunnels
// @Produce json
// @Param id path string true "tunnel id"
// @Success 200 {object} TunnelLogsResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /api/v1/tunnels/{id}/logs [get]
// @Security BearerAuth
func (a *TunnelsAPI) handleTunnelsLogs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		httpError(w, fmt.Errorf("missing tunnel id"), http.StatusBadRequest)
		return
	}

	a.tunnels.mu.Lock()
	t, ok := a.tunnels.tunnels[id]
	a.tunnels.mu.Unlock()

	if !ok {
		httpError(w, fmt.Errorf("tunnel not found"), http.StatusNotFound)
		return
	}

	logs := ""
	if t.logBuf != nil {
		logs = t.logBuf.String()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TunnelLogsResponse{Logs: logs})
}

// handleTunnelsGet lists active local SSH port-forward tunnels.
// @Summary List tunnels
// @Tags tunnels
// @Produce json
// @Success 200 {object} TunnelsListResponse
// @Router /api/v1/tunnels [get]
// @Security BearerAuth
func (a *TunnelsAPI) handleTunnelsGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TunnelsListResponse{Tunnels: a.tunnels.list()})
}

// handleTunnelsPost starts an SSH -L style tunnel in-process.
// @Summary Start tunnel
// @Tags tunnels
// @Accept json
// @Produce json
// @Param body body StartTunnelRequest true "ssh_user, record, mapping (e.g. 8080:localhost:8080)"
// @Success 200 {object} TunnelStartResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/tunnels [post]
// @Security BearerAuth
func (a *TunnelsAPI) handleTunnelsPost(w http.ResponseWriter, r *http.Request) {
	var req StartTunnelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("invalid json"), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Mapping) == "" {
		httpError(w, fmt.Errorf("mapping is required"), http.StatusBadRequest)
		return
	}

	user := a.sshUser(req.SSHUser)
	tunnel := a.tunnels.start(user, req.Record, req.Mapping, a.opts.ExecRegistry)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TunnelStartResponse{Tunnel: *tunnel})
}

// handleTunnelsDelete stops a tunnel by id.
// @Summary Stop tunnel
// @Tags tunnels
// @Produce json
// @Param id path string true "tunnel id"
// @Success 200 {object} TunnelDeleteResponse
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Router /api/v1/tunnels/{id} [delete]
// @Security BearerAuth
func (a *TunnelsAPI) handleTunnelsDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		httpError(w, fmt.Errorf("missing tunnel id"), http.StatusBadRequest)
		return
	}

	if err := a.tunnels.stop(id); err != nil {
		httpError(w, err, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(TunnelDeleteResponse{Success: true})
}
