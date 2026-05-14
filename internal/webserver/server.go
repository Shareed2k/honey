package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/ui"
)

// Options configures the embedded web server.
type Options struct {
	ListenAddr         string // e.g. 127.0.0.1:8765
	Token              string
	ConfigPath         string // optional explicit --config
	Config             *config.File
	RecordDir          string // optional session recording output dir
	LocalFilesRoot     string // optional root for local file browser/upload/download
	AgentBinaryPath    string // optional explicit honey-transfer-agent binary path
	AgentBuildCacheDir string // optional cache dir for auto-built agent binary
	Version            string
	Commit             string
	Date               string
	MaxUploadSize      int64 // default 100 << 20
}

// Server is the honey web UI HTTP server.
type Server struct {
	opts     Options
	mux      *http.ServeMux
	assistRL *slidingRL
	tunnels  *tunnelManager

	assistModelsMu  sync.Mutex
	assistModelIDs  []string
	assistModelsExp time.Time

	fileClientCache *ui.ClientCache

	// pveQemuVncByID holds one-time vncproxy results for /ws/pve-qemu-vnc (see POST /api/v1/pve-qemu-vnc-offer).
	pveQemuVncMu   sync.Mutex
	pveQemuVncByID map[string]pveQemuVncOfferSession
}

// NewServer builds handlers with the given auth token.
func NewServer(opts Options) (*Server, error) {
	if opts.Token == "" {
		return nil, fmt.Errorf("empty auth token")
	}
	if opts.MaxUploadSize <= 0 {
		opts.MaxUploadSize = 100 << 20
	}
	hostexec.ReconfigureFromHoneyConfig(opts.Config)

	s := &Server{
		opts:            opts,
		mux:             http.NewServeMux(),
		assistRL:        newSlidingRL(),
		tunnels:         newTunnelManager(),
		fileClientCache: ui.NewClientCache(),
	}
	s.routes()
	return s, nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/meta", s.withAuth(s.handleMeta))
	s.mux.HandleFunc("GET /api/v1/providers", s.withAuth(s.handleProviders))
	s.mux.HandleFunc("GET /api/v1/backends", s.withAuth(s.handleBackends))
	s.mux.HandleFunc("POST /api/v1/search", s.withAuth(s.handleSearch))
	s.mux.HandleFunc("POST /api/v1/host-ports", s.withAuth(s.handleHostPorts))
	s.mux.HandleFunc("GET /api/v1/tunnels", s.withAuth(s.handleTunnelsGet))
	s.mux.HandleFunc("GET /api/v1/tunnels/{id}/logs", s.withAuth(s.handleTunnelsLogs))
	s.mux.HandleFunc("POST /api/v1/tunnels", s.withAuth(s.handleTunnelsPost))
	s.mux.HandleFunc("DELETE /api/v1/tunnels/{id}", s.withAuth(s.handleTunnelsDelete))
	s.mux.HandleFunc("GET /api/v1/config/backends", s.withAuth(s.handleConfigBackendsGet))
	s.mux.HandleFunc("POST /api/v1/config/backends/{kind}", s.withAuth(s.handleConfigBackendsPost))
	s.mux.HandleFunc("PUT /api/v1/config/backends/{kind}/{index}", s.withAuth(s.handleConfigBackendsPut))
	s.mux.HandleFunc("DELETE /api/v1/config/backends/{kind}/{index}", s.withAuth(s.handleConfigBackendsDelete))
	s.mux.HandleFunc("GET /api/v1/config/schema", s.withAuth(s.handleConfigSchema))
	s.mux.HandleFunc("GET /api/v1/config", s.withAuth(s.handleConfigGet))
	s.mux.HandleFunc("PUT /api/v1/config", s.withAuth(s.handleConfigPut))
	s.mux.HandleFunc("POST /api/v1/upload", s.withAuth(s.handleUpload))
	s.mux.HandleFunc("POST /api/v1/files/local/list", s.withAuth(s.handleFilesLocalList))
	s.mux.HandleFunc("POST /api/v1/files/remote/list", s.withAuth(s.handleFilesRemoteList))
	s.mux.HandleFunc("POST /api/v1/files/copy", s.withAuth(s.handleFilesCopy))
	s.mux.HandleFunc("POST /api/v1/files/agent-transfer", s.withAuth(s.handleFilesAgentTransfer))
	s.mux.HandleFunc("GET /api/v1/recipes", s.withAuth(s.handleRecipesList))
	s.mux.HandleFunc("POST /api/v1/recipes/view", s.withAuth(s.handleRecipesView))
	s.mux.HandleFunc("POST /api/v1/recipes/assist", s.withAuth(s.handleRecipesAssist))
	s.mux.HandleFunc("POST /api/v1/recipes/validate-content", s.withAuth(s.handleRecipesValidateContent))
	s.mux.HandleFunc("POST /api/v1/recipes/parse", s.withAuth(s.handleRecipesParse))
	s.mux.HandleFunc("GET /api/v1/recipes/recent-runs", s.withAuth(s.handleRecipesRecentRuns))
	s.mux.HandleFunc("GET /api/v1/recordings", s.withAuth(s.handleRecordingsList))
	s.mux.HandleFunc("POST /api/v1/recordings/play", s.withAuth(s.handleRecordingsPlay))
	s.mux.HandleFunc("POST /api/v1/exec", s.withAuth(s.handleExec))
	s.mux.HandleFunc("POST /api/v1/cue-exec", s.withAuth(s.handleCueExec))
	s.mux.HandleFunc("POST /api/v1/terminal-assist", s.withAuth(s.handleTerminalAssist))
	s.mux.HandleFunc("GET /api/v1/terminal-assist/models", s.withAuth(s.handleTerminalAssistModels))
	s.mux.HandleFunc("POST /api/v1/pve-qemu-vnc-offer", s.withAuth(s.handlePveQemuVncOffer))
	s.mux.HandleFunc("GET /ws/ssh", s.handleWebSSH)
	s.mux.HandleFunc("GET /ws/pve-qemu-vnc", s.handleWebProxmoxQemuVNC)

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(static)))
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !tokenFromRequest(r, s.opts.Token) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Start listens and serves until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.opts.ListenAddr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		zap.L().Info("honey web listening", zap.String("addr", s.opts.ListenAddr))
		err := srv.ListenAndServe()
		errCh <- err
	}()
	select {
	case <-ctx.Done():
		if s.fileClientCache != nil {
			s.fileClientCache.CloseAll()
		}
		shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shCtx)
		err := <-errCh
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	cfgPath, _ := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version":                     s.opts.Version,
		"commit":                      s.opts.Commit,
		"date":                        s.opts.Date,
		"config_path":                 cfgPath,
		"session_recording_available": strings.TrimSpace(s.opts.RecordDir) != "",
		"terminal_assist_available":   terminalAssistConfigured(),
	})
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	_ = r
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string][]string{
		"providers": searchrun.ListSearchProviderIDs(searchrun.ProviderFlags{}),
	})
}

func (s *Server) handleBackends(w http.ResponseWriter, _ *http.Request) {
	out, err := hostapi.ListBackends(strings.TrimSpace(s.opts.ConfigPath))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var in hostapi.SearchHostsInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.ConfigPath) == "" && strings.TrimSpace(s.opts.ConfigPath) != "" {
		in.ConfigPath = s.opts.ConfigPath
	}
	ctx := r.Context()
	out, err := hostapi.SearchHosts(ctx, &in)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func httpError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
