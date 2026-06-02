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
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/proxy"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/snippets"
	"github.com/shareed2k/honey/internal/ui"
)

// Options configures the embedded web server.
type Options struct {
	ListenAddr         string // e.g. 127.0.0.1:8765
	Token              string
	ConfigPath         string // optional explicit --config
	Config             *config.File
	ExecRegistry       hostexec.Registry
	SearchRegistry     *searchrun.Registry
	RecordDir          string // optional session recording output dir
	LocalFilesRoot     string // optional root for local file browser/upload/download
	AgentBinaryPath    string // optional explicit honey-transfer-agent binary path
	AgentBuildCacheDir string // optional cache dir for auto-built agent binary
	Version            string
	Commit             string
	Date               string
	MaxUploadSize      int64 // default 100 << 20
	MetricsListenAddr  string
	Metrics            *metrics.Registry
	NoCache            bool
	Refresh            bool
	AllowLogsCommand   bool
}

// Server is the honey web UI HTTP server.
type Server struct {
	opts     Options
	metrics  *metrics.Registry
	mux      *http.ServeMux
	assistRL *slidingRL
	tunnels  *tunnelManager
	proxy    *proxy.Manager
	pgPools  *postgres.PoolManager

	assistModelsMu  sync.Mutex
	assistModelIDs  []string
	assistModelsExp time.Time

	fileClientCache *ui.ClientCache

	snippetStore snippets.Store

	retentionState recordingRetentionState

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
	if opts.ExecRegistry != nil {
		opts.ExecRegistry.Reconfigure(opts.Config)
	}

	s := &Server{
		opts:            opts,
		metrics:         opts.Metrics,
		mux:             http.NewServeMux(),
		assistRL:        newSlidingRL(),
		tunnels:         newTunnelManager(),
		proxy:           proxy.NewManager(proxy.NewLogger(zap.L())),
		pgPools:         postgres.NewPoolManager(),
		fileClientCache: ui.NewClientCache(),
	}
	s.fileClientCache.SetRegistry(opts.ExecRegistry)
	s.snippetStore = snippets.NewLocalStore(snippetsFilePath(opts.ConfigPath))
	if err := s.routes(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) routes() error {
	s.mux.HandleFunc("GET /api/v1/meta", s.withAuth(s.handleMeta))
	s.mux.HandleFunc("GET /api/v1/openapi.json", s.withAuth(s.handleOpenAPIJSON))
	s.mux.HandleFunc("GET /api/v1/providers", s.withAuth(s.handleProviders))
	s.mux.HandleFunc("GET /api/v1/backends", s.withAuth(s.handleBackends))
	s.mux.HandleFunc("GET /api/v1/logs/default", s.withAuth(s.handleLogsDefault))
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
	s.mux.HandleFunc("POST /api/v1/recipes/graph-plan", s.withAuth(s.handleRecipesGraphPlan))
	s.mux.HandleFunc("POST /api/v1/recipes/parse", s.withAuth(s.handleRecipesParse))
	s.mux.HandleFunc("GET /api/v1/recipes/recent-runs", s.withAuth(s.handleRecipesRecentRuns))
	s.mux.HandleFunc("GET /api/v1/recordings", s.withAuth(s.handleRecordingsList))
	s.mux.HandleFunc("POST /api/v1/recordings/play", s.withAuth(s.handleRecordingsPlay))
	s.mux.HandleFunc("DELETE /api/v1/recordings/{file_name}", s.withAuth(s.handleRecordingsDelete))
	s.mux.HandleFunc("POST /api/v1/recordings/summarize", s.withAuth(s.handleRecordingsSummarize))
	s.mux.HandleFunc("POST /api/v1/exec", s.withAuth(s.handleExec))
	s.mux.HandleFunc("POST /api/v1/lint", s.withAuth(s.handleLint))
	s.mux.HandleFunc("GET /api/v1/snippets", s.withAuth(s.handleSnippetsList))
	s.mux.HandleFunc("POST /api/v1/snippets", s.withAuth(s.handleSnippetsSave))
	s.mux.HandleFunc("DELETE /api/v1/snippets/{id}", s.withAuth(s.handleSnippetsDelete))
	s.mux.HandleFunc("POST /api/v1/cue-exec", s.withAuth(s.handleCueExec))
	s.mux.HandleFunc("POST /api/v1/terminal-assist", s.withAuth(s.handleTerminalAssist))
	s.mux.HandleFunc("GET /api/v1/terminal-assist/models", s.withAuth(s.handleTerminalAssistModels))
	s.mux.HandleFunc("POST /api/v1/pve-qemu-vnc-offer", s.withAuth(s.handlePveQemuVncOffer))
	s.mux.HandleFunc("GET /ws/ssh", s.handleWebSSH)
	s.mux.HandleFunc("GET /ws/pve-qemu-vnc", s.handleWebProxmoxQemuVNC)

	s.mux.HandleFunc("GET /api/v1/apps", s.withAuth(s.handleAppsList))
	s.mux.HandleFunc("GET /api/v1/proxy/sessions", s.withAuth(s.handleProxySessionsGet))
	s.mux.HandleFunc("POST /api/v1/proxy/start", s.withAuth(s.handleProxySessionStart))
	s.mux.HandleFunc("DELETE /api/v1/proxy/sessions/{id}", s.withAuth(s.handleProxySessionDelete))
	s.mux.HandleFunc("GET /api/v1/postgres/catalog", s.withAuth(s.handlePostgresCatalog))
	s.mux.HandleFunc("POST /api/v1/postgres/query", s.withAuth(s.handlePostgresQuery))

	s.mux.HandleFunc("POST /api/v1/logs/stream", s.withAuth(s.handleLogsStream))

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("mount embedded static assets: %w", err)
	}
	s.mux.Handle("/", http.FileServer(http.FS(static)))
	return nil
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
	handler := http.Handler(s.mux)
	if s.metrics != nil {
		handler = s.metrics.Middleware(handler)
	}

	// Add the subdomain proxy wrapper at the very top level
	handler = s.subdomainProxyWrapper(handler)

	srv := &http.Server{
		Addr:              s.opts.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	var metricsSrv *http.Server
	errCh := make(chan error, 2)
	if addr := strings.TrimSpace(s.opts.MetricsListenAddr); addr != "" && s.metrics != nil {
		metricsSrv = &http.Server{
			Addr:              addr,
			Handler:           s.metrics.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			zap.L().Info("honey metrics listening", zap.String("addr", addr))
			errCh <- metricsSrv.ListenAndServe()
		}()
	}

	go func() {
		zap.L().Info("honey web listening", zap.String("addr", s.opts.ListenAddr))
		errCh <- srv.ListenAndServe()
	}()

	s.startRecordingRetention(ctx)

	nListeners := 1
	if metricsSrv != nil {
		nListeners = 2
	}
	for {
		select {
		case <-ctx.Done():
			if s.fileClientCache != nil {
				s.fileClientCache.CloseAll()
			}
			if s.pgPools != nil {
				s.pgPools.Close()
			}
			shCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = srv.Shutdown(shCtx)
			if metricsSrv != nil {
				_ = metricsSrv.Shutdown(shCtx)
			}
			cancel()
			for i := 0; i < nListeners; i++ {
				err := <-errCh
				if err != nil && err != http.ErrServerClosed {
					return err
				}
			}
			return nil
		case err := <-errCh:
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		}
	}
}

// handleMeta returns server build metadata and feature flags.
// @Summary Server metadata
// @Tags meta
// @Produce json
// @Success 200 {object} MetaResponse
// @Router /api/v1/meta [get]
// @Security BearerAuth
func (s *Server) handleMeta(w http.ResponseWriter, _ *http.Request) {
	cfgPath, _ := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	w.Header().Set("Content-Type", "application/json")
	meta := MetaResponse{
		Version:                   s.opts.Version,
		Commit:                    s.opts.Commit,
		Date:                      s.opts.Date,
		ConfigPath:                cfgPath,
		SessionRecordingAvailable: strings.TrimSpace(s.opts.RecordDir) != "",
		TerminalAssistAvailable:   terminalAssistConfigured(),
		LogsCommandAllowed:        s.opts.AllowLogsCommand,
	}
	if maxAge, text := s.recordingRetentionMaxAge(); maxAge > 0 && text != "" {
		meta.SessionRecordingRetention = text
	}
	s.retentionState.mu.Lock()
	if !s.retentionState.lastPurgeAt.IsZero() {
		meta.SessionRecordingLastPurge = s.retentionState.lastPurgeAt.Format(time.RFC3339)
	}
	s.retentionState.mu.Unlock()
	if addr := strings.TrimSpace(s.opts.MetricsListenAddr); addr != "" {
		meta.MetricsURL = "http://" + addr + "/metrics"
	}
	_ = json.NewEncoder(w).Encode(meta)
}

// handleProviders returns supported provider IDs for search.
// @Summary List search provider IDs
// @Tags meta
// @Produce json
// @Success 200 {object} ProvidersResponse
// @Router /api/v1/providers [get]
// @Security BearerAuth
func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	_ = r
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ProvidersResponse{
		Providers: s.opts.SearchRegistry.ListSearchProviderIDs(searchrun.ProviderOverrides{}),
	})
}

// handleBackends lists backends from the active honey config.
// @Summary List configured backends
// @Tags meta
// @Produce json
// @Success 200 {object} hostapi.ListBackendsOutput
// @Failure 400 {object} map[string]string
// @Router /api/v1/backends [get]
// @Security BearerAuth
func (s *Server) handleBackends(w http.ResponseWriter, _ *http.Request) {
	out, err := hostapi.ListBackends(strings.TrimSpace(s.opts.ConfigPath), s.opts.SearchRegistry)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// handleSearch runs the same parallel search as the CLI/TUI.
// @Summary Search hosts
// @Tags search
// @Accept json
// @Produce json
// @Param body body hostapi.SearchHostsInput true "search request"
// @Success 200 {object} hostapi.SearchHostsOutput
// @Failure 400 {object} map[string]string
// @Router /api/v1/search [post]
// @Security BearerAuth
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	var in hostapi.SearchHostsInput
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.ConfigPath) == "" && strings.TrimSpace(s.opts.ConfigPath) != "" {
		in.ConfigPath = s.opts.ConfigPath
	}
	if s.opts.NoCache {
		in.NoCache = true
	}
	if s.opts.Refresh {
		in.Refresh = true
	}
	in.SSHUser = s.sshUser(in.SSHUser)
	ctx := r.Context()
	start := time.Now()
	out, err := hostapi.SearchHosts(ctx, &in, s.opts.ExecRegistry, s.opts.SearchRegistry)
	if s.metrics != nil {
		n := 0
		if err == nil {
			n = len(out.Records)
		}
		s.metrics.ObserveSearch(err, time.Since(start), n)
	}
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

func (s *Server) sshUser(requested string) string {
	user := strings.TrimSpace(requested)
	if user == "" {
		if cfg := s.opts.Config; cfg != nil && cfg.Defaults.SSHUser != "" {
			user = cfg.Defaults.SSHUser
		}
	}
	return user
}
