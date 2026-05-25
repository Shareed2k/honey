package webserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/proxy"
	"github.com/shareed2k/honey/internal/sshclient"
)

type proxyStartRequest struct {
	App       string `json:"app"`
	SSHUser   string `json:"ssh_user,omitempty"`
	Providers string `json:"providers,omitempty"`
	Backends  string `json:"backends,omitempty"`
}

func resolveAppDialer(ctx context.Context, _ *config.File, configPath string, app apps.AppConfig, sshUser string, req proxyStartRequest) (proxy.Dialer, io.Closer, error) {
	// First run a search to find the record for this target
	in := hostapi.SearchHostsInput{
		Name:       app.Target,
		NameRegex:  app.TargetRegex,
		ConfigPath: configPath,
		SSHUser:    sshUser,
		Providers:  req.Providers,
		Backends:   req.Backends,
	}

	out, err := hostapi.SearchHosts(ctx, &in)
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

	rec := out.Records[0]
	ip := strings.TrimSpace(rec.PrimaryIP)

	useSSH := ip != "" && (rec.Provider != "k8s" || rec.Meta["kind"] != "pod")
	if !useSSH {
		td, err := proxy.NewTunnelDialer(ctx, sshUser, rec, app.Upstream)
		return td, nil, err
	}

	if ip == "" {
		if app.TargetRegex != "" {
			return nil, nil, fmt.Errorf("target regex %q has no primary IP", app.TargetRegex)
		}
		return nil, nil, fmt.Errorf("target %q has no primary IP", app.Target)
	}

	sshPort := 0
	if p, ok := hosts.MetaSSHPort(&rec); ok {
		sshPort = p
	}
	identity := ""
	if id, ok := hosts.MetaSSHIdentityFile(&rec); ok {
		identity = id
	}

	client, err := sshclient.DialHoneyClient(sshUser, ip, sshPort, identity)
	if err != nil {
		return nil, nil, err
	}
	return &proxy.SSHDialer{Client: client}, client, nil
}

func (s *Server) handleAppsList(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Config == nil || s.opts.Config.Apps == nil {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"apps": map[string]apps.AppConfig{}})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"apps": s.opts.Config.Apps,
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
		_ = json.NewEncoder(w).Encode(sess)
		return
	}

	// Resolve the target for SSH Dialing
	dialer, closer, err := resolveAppDialer(r.Context(), s.opts.Config, s.opts.ConfigPath, app, req.SSHUser, req)
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
	_ = json.NewEncoder(w).Encode(sess)
}

func (s *Server) handleProxySessionDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.proxy.Stop(id); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": %q}`, err.Error()), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
