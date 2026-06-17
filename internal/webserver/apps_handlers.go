package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/appsecret"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/proxy"
	"github.com/shareed2k/honey/internal/searchrun"
)

const encryptedUpstreamRedaction = "[encrypted]"

func sanitizeAppForAPI(app apps.AppConfig) apps.AppConfig {
	if appsecret.IsEncryptedUpstream(app.Upstream) {
		app.Upstream = encryptedUpstreamRedaction
	}
	return app
}

func sanitizeSessionForAPI(cfg *config.File, sess proxy.Session) proxy.Session {
	if cfg != nil && cfg.Apps != nil {
		if src, ok := cfg.Apps[sess.App.Name]; ok && appsecret.IsEncryptedUpstream(src.Upstream) {
			sess.App.Upstream = encryptedUpstreamRedaction
			return sess
		}
	}
	sess.App = sanitizeAppForAPI(sess.App)
	return sess
}

type proxyStartRequest struct {
	App       string `json:"app"`
	SSHUser   string `json:"ssh_user,omitempty"`
	Providers string `json:"providers,omitempty"`
	Backends  string `json:"backends,omitempty"`
}

func resolveAppDialer(ctx context.Context, _ *config.File, configPath string, app apps.AppConfig, sshUser string, req proxyStartRequest, cache *engine.ClientCache, reg hostexec.Registry, searchReg *searchrun.Registry) (proxy.Dialer, io.Closer, error) {
	// First run a search to find the record for this target
	in := hostapi.SearchHostsInput{
		Name:       app.Target,
		NameRegex:  app.TargetRegex,
		ConfigPath: configPath,
		SSHUser:    sshUser,
		Providers:  req.Providers,
		Backends:   req.Backends,
	}

	out, err := hostapi.SearchHosts(ctx, &in, reg, searchReg)
	if err != nil {
		if app.TargetRegex != "" {
			return nil, nil, fmt.Errorf("resolve target (regex %q): %w", app.TargetRegex, err)
		}
		return nil, nil, fmt.Errorf("resolve target %q: %w", app.Target, err)
	}

	if len(out.Records) == 0 {
		if app.TargetRegex != "" {
			return nil, nil, fmt.Errorf("target regex %q not found", app.TargetRegex)
		}
		return nil, nil, fmt.Errorf("target %q not found", app.Target)
	}

	return engine.ResolveAppDialerWithCache(sshUser, out.Records[0], cache)
}

func (s *Server) handleAppsList(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Config == nil || s.opts.Config.Apps == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"apps": map[string]apps.AppConfig{}})
		return
	}
	appsOut := make(map[string]apps.AppConfig, len(s.opts.Config.Apps))
	for name, app := range s.opts.Config.Apps {
		appsOut[name] = sanitizeAppForAPI(app)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"apps": appsOut,
	})
}

func (s *Server) handleProxySessionsGet(w http.ResponseWriter, _ *http.Request) {
	sessions, err := s.proxy.List()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	if sessions == nil {
		sessions = []proxy.Session{}
	}
	for i := range sessions {
		sessions[i] = sanitizeSessionForAPI(s.opts.Config, sessions[i])
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": sessions,
	})
}

func (s *Server) handleProxySessionStart(w http.ResponseWriter, r *http.Request) {
	var req proxyStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid request"}`, http.StatusBadRequest)
		return
	}

	if s.opts.Config == nil || s.opts.Config.Apps == nil {
		http.Error(w, `{"error": "no apps configured"}`, http.StatusBadRequest)
		return
	}

	app, ok := s.opts.Config.Apps[req.App]
	if !ok {
		http.Error(w, fmt.Sprintf(`{"error": "app %s not found"}`, req.App), http.StatusNotFound)
		return
	}

	// Override the request filters if the app configuration hardcodes them
	if app.Backend != "" {
		req.Backends = app.Backend
	}
	if app.Provider != "" {
		req.Providers = app.Provider
	}
	upstreamWasEncrypted := appsecret.IsEncryptedUpstream(app.Upstream)
	resolvedUpstream, err := appsecret.ResolveUpstream(r.Context(), s.opts.Config, app.Upstream)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusBadRequest)
		return
	}
	app.Upstream = resolvedUpstream

	// Always override LocalPort to 0 when starting via Web UI.
	// We want HTTP requests to flow over the webserver's subdomain routing
	// endpoint, instead of spinning up new listeners locally.
	if app.Type == apps.AppTypeHTTP {
		app.LocalPort = 0
	}

	if app.Target == "" && app.TargetRegex == "" {
		// Use direct dialer
		sess, err := s.proxy.Start(context.Background(), app, proxy.DirectDialer{}, nil)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := sanitizeSessionForAPI(s.opts.Config, *sess)
		if upstreamWasEncrypted {
			out.App.Upstream = encryptedUpstreamRedaction
		}
		_ = json.NewEncoder(w).Encode(out)
		return
	}

	// Resolve the target for SSH Dialing
	dialer, closer, err := resolveAppDialer(r.Context(), s.opts.Config, s.opts.ConfigPath, app, req.SSHUser, req, s.fileClientCache, s.opts.ExecRegistry, s.opts.SearchRegistry)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	sess, err := s.proxy.Start(context.Background(), app, dialer, closer)
	if err != nil {
		if closer != nil {
			_ = closer.Close() // Cleanup if start failed
		}
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	out := sanitizeSessionForAPI(s.opts.Config, *sess)
	if upstreamWasEncrypted {
		out.App.Upstream = encryptedUpstreamRedaction
	}
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleProxySessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.proxy.Stop(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
