package webserver

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/approval"
	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/meshnet"
	"github.com/shareed2k/honey/internal/metrics"
	plugincache "github.com/shareed2k/honey/internal/plugincache"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/postgres"
	"github.com/shareed2k/honey/internal/proxy"
	"github.com/shareed2k/honey/internal/queue"
	"github.com/shareed2k/honey/internal/scheduler"
	"github.com/shareed2k/honey/internal/searchrun"
	"github.com/shareed2k/honey/internal/snippets"
	"github.com/shareed2k/honey/internal/webauthn"
	"github.com/shareed2k/honey/internal/webserver/workspacestore"
)

// Options configures the embedded web server.
type Options struct {
	ListenAddr         string // e.g. 127.0.0.1:8765
	Token              string
	DisableAuth        bool   // when true, skip token auth entirely (trusted networks / authenticating proxy)
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
	// EnableMesh, when true, additionally serves this webserver's existing API
	// on a second listener obtained from internal/meshnet.Listener() — so other
	// honey instances can reach this one through the libp2p mesh (Circuit Relay
	// v2 + DCUtR), in addition to (not instead of) the normal TCP ListenAddr.
	// A misconfigured or not-yet-ready mesh must never prevent the ordinary
	// TCP listener from serving — see Start's handling below.
	EnableMesh bool
	OnReady    func() // called after the listener is bound, before serving

	// AuditSink receives one event per security-relevant action (approval decisions,
	// recipe runs). nil is replaced with a no-op sink in NewServer.
	AuditSink audit.Sink

	// JWTPubKey, when non-nil, enables Ed25519 JWT identity resolution: a valid
	// bearer JWT's subject claim becomes the request actor. nil disables JWT.
	JWTPubKey ed25519.PublicKey
	// TrustedProxyNets lists peer networks allowed to assert caller identity via
	// the X-Honey-User header. nil disables the trusted-header path.
	TrustedProxyNets []*net.IPNet
	// Enforcer, when non-nil, gates every authenticated API request through OPA.
	// nil disables the API policy gate.
	Enforcer *policy.Enforcer
	// Approvals holds pending require_approval runs. When nil, NewServer creates a
	// default in-memory store so the approval endpoints and recipe gate share one.
	Approvals *approval.Store
	// WebAuthn, when non-nil, enables passkey biometric step-up for
	// require_biometric verdicts and the /api/v1/webauthn/* endpoints.
	WebAuthn *webauthn.Manager

	// WebhookRatePerSecond and WebhookBurst control the per-app-name rate limit on
	// unauthenticated webhook endpoints. Defaults: 10 req/s, burst 20.
	WebhookRatePerSecond float64
	WebhookBurst         int
}

// Server is the honey web UI HTTP server.
type Server struct {
	opts     Options
	metrics  *metrics.Registry
	router   chi.Router
	assistRL *slidingRL
	tunnels  *tunnelManager
	proxy    *proxy.Manager
	pgPools  *postgres.PoolManager

	webhookQueue    queue.Queue
	plugins         *plugincache.Cache
	scheduleManager *scheduler.Manager

	assistModelsMu  sync.Mutex
	assistModelIDs  []string
	assistModelsExp time.Time

	fileClientCache *engine.ClientCache

	snippetStore snippets.Store
	workspace    workspaceStore

	retentionState recordingRetentionState

	// pveQemuVncByID holds one-time vncproxy results for /ws/pve-qemu-vnc (see POST /api/v1/pve-qemu-vnc-offer).
	pveQemuVncMu   sync.Mutex
	pveQemuVncByID map[string]pveQemuVncOfferSession

	recipesAPI *RecipesAPI

	commandRunner *engine.CommandRunner

	// deviceCA + enroll back the mTLS device-enrollment endpoints. Both are nil
	// when no state dir is available (enrollment disabled).
	deviceCA *DeviceCA
	enroll   *enrollStore

	// remoteListenerFor obtains the reverse listener on the target side for the
	// /api/v1/ws/remote-forward handler. nil selects defaultRemoteListener (the
	// leaf.Listen path); tests inject an in-memory listener to avoid real SSH.
	remoteListenerFor func(user string, r hosts.Record, bind string, port int) (net.Listener, func(), error)

	// udpDialer obtains the UDP target connection for the /api/v1/ws/udp
	// handler. Defaulted to realUDPDialer{} below; tests inject a fake to
	// avoid opening real UDP sockets.
	udpDialer udpDialer
}

// NewServer builds handlers with the given auth token.
func NewServer(opts Options) (*Server, error) {
	if opts.Token == "" && !opts.DisableAuth {
		return nil, fmt.Errorf("empty auth token")
	}
	if opts.MaxUploadSize <= 0 {
		opts.MaxUploadSize = 100 << 20
	}
	if opts.Approvals == nil {
		opts.Approvals = approval.NewStore(24 * time.Hour)
	}
	if opts.AuditSink == nil {
		if opts.Config != nil && opts.Config.Audit.Enabled {
			path := opts.Config.Audit.EffectivePath()
			if s, err := audit.NewFileSink(path); err != nil {
				zap.L().Warn("audit: failed to open log file", zap.String("path", path), zap.Error(err))
				opts.AuditSink = audit.NewNoopSink()
			} else {
				opts.AuditSink = s
			}
		} else {
			opts.AuditSink = audit.NewNoopSink()
		}
	}
	if opts.ExecRegistry != nil {
		opts.ExecRegistry.Reconfigure(opts.Config)
	}

	q, err := queue.NewAntsQueue(50)
	if err != nil {
		return nil, fmt.Errorf("init webhook queue: %w", err)
	}

	pgPools := postgres.NewPoolManager()
	pc := plugincache.New(opts.Config)
	s := &Server{
		opts:            opts,
		metrics:         opts.Metrics,
		router:          chi.NewRouter(),
		assistRL:        newSlidingRL(),
		tunnels:         newTunnelManager(),
		proxy:           proxy.NewManager(proxy.NewLogger(zap.L())),
		pgPools:         pgPools,
		webhookQueue:    q,
		plugins:         pc,
		fileClientCache: engine.NewClientCache(),
		udpDialer:       realUDPDialer{},
		commandRunner: engine.NewCommandRunner(engine.CommandRunnerOptions{
			ExecRegistry:   opts.ExecRegistry,
			SearchRegistry: opts.SearchRegistry,
			Metrics:        opts.Metrics,
			RecordDir:      opts.RecordDir,
		}),
	}
	if opts.Config != nil {
		schedMgr, err := scheduler.New(scheduler.Options{
			ConfigPath:     opts.ConfigPath,
			Config:         opts.Config,
			RecordDir:      opts.RecordDir,
			ExecRegistry:   opts.ExecRegistry,
			SearchRegistry: opts.SearchRegistry,
			Queue:          q,
			Metrics:        opts.Metrics,
			Pools:          pgPools,
			Cache:          s.fileClientCache,
			Enforcer:       opts.Enforcer,
		})
		if err != nil {
			zap.L().Warn("scheduler init failed, schedules disabled", zap.Error(err))
		} else {
			s.scheduleManager = schedMgr
		}
	}
	s.fileClientCache.SetRegistry(opts.ExecRegistry)
	s.snippetStore = snippets.NewLocalStore(snippetsFilePath(opts.ConfigPath))

	s.workspace = workspacestore.New(workspaceStoreDir(s.opts.ConfigPath))

	// Device mTLS enrollment: load-or-create a device CA under the state dir.
	// Non-fatal — endpoints report 503 when unavailable.
	if stateDir, derr := config.ResolveStateDir(); derr == nil && strings.TrimSpace(stateDir) != "" {
		if ca, caErr := LoadOrCreateDeviceCA(stateDir); caErr == nil {
			s.deviceCA = ca
			s.enroll = newEnrollStore()
		}
	}

	if err := s.routes(); err != nil {
		return nil, err
	}
	return s, nil
}

// workspaceStoreDir returns the dir for the studio workspace store: beside
// the resolved config file when one exists, else under the runtime state
// dir. Mirrors snippetsFilePath's tiering (see snippets_handlers.go).
func workspaceStoreDir(configPath string) string {
	if cp, err := config.ResolvePath(strings.TrimSpace(configPath)); err == nil && cp != "" {
		return filepath.Dir(cp)
	}
	if dir, err := config.ResolveStateDir(); err == nil && dir != "" {
		return dir
	}
	return "."
}

func (s *Server) routes() error {
	s.recipesAPI = NewRecipesAPI(s.opts, s.metrics, s.webhookQueue, s.pgPools, s, s.plugins, s.fileClientCache)
	recipesAPI := s.recipesAPI

	s.router.Route("/api/v1", func(r chi.Router) {
		r.Use(s.authMiddleware)

		r.Mount("/recipes", recipesAPI.Routes())

		r.Get("/meta", s.handleMeta)
		r.Get("/openapi.json", s.handleOpenAPIJSON)
		r.Get("/approvals", s.handleListApprovals)
		r.Post("/approvals/{id}", s.handleDecideApproval)
		r.Post("/webauthn/register/begin", s.handleWebAuthnRegisterBegin)
		r.Post("/webauthn/register/finish", s.handleWebAuthnRegisterFinish)
		r.Post("/webauthn/assert/begin", s.handleWebAuthnAssertBegin)
		r.Post("/webauthn/assert/finish", s.handleWebAuthnAssertFinish)
		r.Get("/providers", s.handleProviders)
		r.Get("/backends", s.handleBackends)
		r.Get("/logs/default", s.handleLogsDefault)
		r.Post("/search", s.handleSearch)
		r.Post("/secrets/encrypt", s.handleSecretsEncrypt)
		r.Post("/secrets/seal", s.handleSecretsSeal)
		r.Post("/host-ports", s.handleHostPorts)

		r.Route("/tunnels", func(tr chi.Router) {
			tr.Get("/", s.handleTunnelsGet)
			tr.Get("/{id}/logs", s.handleTunnelsLogs)
			tr.Post("/", s.handleTunnelsPost)
			tr.Delete("/{id}", s.handleTunnelsDelete)
		})

		r.Route("/config", func(cr chi.Router) {
			cr.Get("/backends", s.handleConfigBackendsGet)
			cr.Post("/backends/{kind}", s.handleConfigBackendsPost)
			cr.Put("/backends/{kind}/{index}", s.handleConfigBackendsPut)
			cr.Delete("/backends/{kind}/{index}", s.handleConfigBackendsDelete)
			cr.Get("/schema", s.handleConfigSchema)
			cr.Get("/", s.handleConfigGet)
			cr.Put("/", s.handleConfigPut)
		})

		r.Post("/upload", s.handleUpload)
		r.Route("/files", func(fr chi.Router) {
			fr.Post("/local/list", s.handleFilesLocalList)
			fr.Post("/remote/list", s.handleFilesRemoteList)
			fr.Post("/copy", s.handleFilesCopy)
			fr.Post("/agent-transfer", s.handleFilesAgentTransfer)
			fr.Post("/remote/stat", s.handleFilesRemoteStat)
			fr.Post("/remote/mkdir", s.handleFilesRemoteMkdir)
			fr.Post("/remote/remove", s.handleFilesRemoteRemove)
			fr.Post("/remote/upload", s.handleFilesRemoteUpload)
			fr.Get("/remote/download", s.handleFilesRemoteDownload)
		})

		r.Route("/recordings", func(rcr chi.Router) {
			rcr.Get("/", s.handleRecordingsList)
			rcr.Post("/play", s.handleRecordingsPlay)
			rcr.Delete("/{file_name}", s.handleRecordingsDelete)
			rcr.Post("/summarize", s.handleRecordingsSummarize)
			rcr.Get("/{id}/failed-hosts", s.handleRecordingsFailedHosts)
		})

		r.Post("/exec", s.handleExec)
		r.Post("/lint", s.handleLint)
		r.Route("/snippets", func(snr chi.Router) {
			snr.Get("/", s.handleSnippetsList)
			snr.Post("/", s.handleSnippetsSave)
			snr.Delete("/{id}", s.handleSnippetsDelete)
		})

		r.Post("/cue-exec", recipesAPI.handleCueExec)
		r.Post("/terminal-assist", s.handleTerminalAssist)
		r.Get("/terminal-assist/models", s.handleTerminalAssistModels)
		r.Post("/pve-qemu-vnc-offer", s.handlePveQemuVncOffer)
		r.Get("/ws/tunnel", s.handleWebTunnel)
		r.Get("/ws/remote-forward", s.handleWebRemoteForward)
		r.Get("/ws/udp", s.handleWebUDPRelay)
		r.Get("/ws/exec", s.handleWebExec)

		r.Post("/agent", s.handleAgent)
		r.Get("/apps", s.handleAppsList)
		r.Get("/schedules", s.handleSchedulesList)

		r.Route("/proxy", func(pr chi.Router) {
			pr.Get("/sessions", s.handleProxySessionsGet)
			pr.Post("/start", s.handleProxySessionStart)
			pr.Delete("/sessions/{id}", s.handleProxySessionDelete)
		})

		r.Route("/postgres", func(pgr chi.Router) {
			pgr.Get("/catalog", s.handlePostgresCatalog)
			pgr.Post("/query", s.handlePostgresQuery)
		})

		r.Route("/logs", func(lr chi.Router) {
			lr.Post("/stream", s.handleLogsStream)
			lr.Get("/feedback", s.handleLogsFeedbackGet)
			lr.Post("/feedback", s.handleLogsFeedbackSave)
			lr.Post("/feedback/suggest", s.handleLogsFeedbackSuggest)
			lr.Post("/rca", s.handleLogsRCA)
			lr.Post("/summary", s.handleLogsSummary)
		})

		// Webhook results need auth
		r.Get("/webhooks/results/{id}", recipesAPI.handleRecipeWebhookResult)

		// Webhook debugging (web UI, authenticated): test-send + delivery inspection.
		r.Post("/webhooks/{app_name}/{webhook_name}/debug", recipesAPI.handleWebhookDebug)
		r.Get("/webhooks/{app_name}/{webhook_name}/deliveries", recipesAPI.handleWebhookDeliveries)

		// Device mTLS enrollment: mint a one-time code (operator) + list issued devices.
		r.Post("/devices/enroll-code", s.handleMintEnrollCode)
		r.Get("/devices", s.handleListDevices)

		r.Route("/studio", func(sr chi.Router) {
			sr.Get("/workspace", s.handleGetStudioWorkspace)
			sr.Put("/workspace", s.handlePutStudioWorkspace)
		})
	})

	// Device enrollment is authenticated by the one-time code, not the session
	// token, so it mounts outside the main auth group.
	s.router.Post("/api/v1/devices/enroll", s.handleDeviceEnroll)

	// Webhooks have their own custom auth, so they mount outside the main /api/v1 auth group
	s.router.With(recipesAPI.webhookRateLimit).
		Post("/api/v1/webhooks/{app_name}/{webhook_name}", recipesAPI.handleRecipeWebhook)

	s.router.Get("/ws/ssh", s.handleWebSSH)
	s.router.Get("/ws/pve-qemu-vnc", s.handleWebProxmoxQemuVNC)

	static, err := fs.Sub(staticFS, "static")
	if err != nil {
		return fmt.Errorf("mount embedded static assets: %w", err)
	}
	s.router.Handle("/*", s.withIndexCookie(http.FileServer(http.FS(static))))
	return nil
}

// authorized reports whether the request may access protected resources: either
// auth is disabled, or the request carries a valid token (header/query/cookie).
func (s *Server) authorized(r *http.Request) bool {
	return s.opts.DisableAuth || tokenFromRequest(r, s.opts.Token)
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		actor := userFromRequest(r, s.opts.TrustedProxyNets, s.opts.JWTPubKey)
		r = r.WithContext(context.WithValue(r.Context(), ctxActorKey, actor))

		if s.opts.Enforcer != nil {
			d, err := s.opts.Enforcer.Evaluate(r.Context(), map[string]any{
				"action": "api_request",
				"actor":  actor,
				"method": r.Method,
				"path":   r.URL.Path,
			})
			if err != nil || !d.Allow {
				reason := "forbidden by policy"
				if d.DenyReason != "" {
					reason = d.DenyReason
				}
				http.Error(w, `{"error":`+strconv.Quote(reason)+`}`, http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// withAuth is a wrapper for tests that directly invoke handler functions
func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.authorized(r) {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) allowAssistRequest(ip string, maxRPM int) bool {
	return s.assistRL.allow(ip, maxRPM)
}

// withIndexCookie serves the static UI and, when a page is opened with a valid
// ?token= query, also persists the token in the honey_proxy_token cookie. The token
// is intentionally left in the URL (not stripped/redirected) so the SPA can read it
// into sessionStorage — unlike the subdomain proxy, the UI itself is the consumer.
// The cookie is a bonus: it authorizes subsequent REST/WS requests (e.g. a bare URL
// in docker) via the cookie branch of tokenFromRequest. No-op when auth is disabled.
func (s *Server) withIndexCookie(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.opts.DisableAuth && r.Method == http.MethodGet {
			if q := strings.TrimSpace(r.URL.Query().Get("token")); q != "" && tokenFromRequest(r, s.opts.Token) {
				setTokenCookie(w, r, q)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Start listens and serves until ctx is cancelled.
func (s *Server) Start(ctx context.Context) error {
	handler := http.Handler(s.router)
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

	ln, err := net.Listen("tcp", s.opts.ListenAddr)
	if err != nil {
		return err
	}
	if s.opts.OnReady != nil {
		s.opts.OnReady()
	}
	go func() {
		zap.L().Info("honey web listening", zap.String("addr", s.opts.ListenAddr))
		errCh <- srv.Serve(ln)
	}()

	var meshLn net.Listener
	if s.opts.EnableMesh {
		meshLn, err = meshnet.Listener()
		if err != nil {
			zap.L().Warn("honey mesh listener unavailable, continuing without it", zap.Error(err))
			meshLn = nil
		} else {
			go func() {
				zap.L().Info("honey web listening (mesh)")
				errCh <- srv.Serve(meshLn)
			}()
		}
	}

	s.startRecordingRetention(ctx)
	if s.scheduleManager != nil {
		s.scheduleManager.Start(ctx)
	}

	nListeners := 1
	if metricsSrv != nil {
		nListeners++
	}
	if meshLn != nil {
		nListeners++
	}
	for {
		select {
		case <-ctx.Done():
			if s.webhookQueue != nil {
				s.webhookQueue.Close()
			}
			if s.plugins != nil {
				s.plugins.Close()
			}
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
