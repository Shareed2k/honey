package webserver

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/searchrun"

	// Register search providers for ListSearchProviderIDs / factories.
	"github.com/shareed2k/honey/internal/provider/all"
	_ "github.com/shareed2k/honey/internal/provider/awsprovider"
	_ "github.com/shareed2k/honey/internal/provider/consulprovider"
	_ "github.com/shareed2k/honey/internal/provider/gcp"
	_ "github.com/shareed2k/honey/internal/provider/k8sprovider"
	_ "github.com/shareed2k/honey/internal/provider/proxmoxprovider"
)

func TestProvidersEndpoint(t *testing.T) {
	s, err := NewServer(Options{
		ListenAddr:     "127.0.0.1:0",
		Token:          "tok",
		Version:        "0",
		SearchRegistry: searchrun.NewRegistry(all.Factories(all.Deps{})),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/providers", nil)
	req.Header.Set("Authorization", "Bearer tok")
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", rec.Code, rec.Body.String())
	}
	var out struct {
		Providers []string `json:"providers"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Providers) == 0 {
		t.Fatal("expected non-empty providers")
	}
}

func TestConfigBackendsCRUD(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	initial := "version: 1\nbackends:\n  gcp:\n    - name: one\n      project: p1\n      zone: z1\n"
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewServer(Options{
		ListenAddr:     "127.0.0.1:0",
		Token:          "secret",
		ConfigPath:     cfgPath,
		SearchRegistry: searchrun.NewRegistry(all.Factories(all.Deps{})),
	})
	if err != nil {
		t.Fatal(err)
	}

	auth := func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer secret")
	}

	// GET
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/v1/config/backends", nil)
		auth(req)
		s.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET backends %d: %s", rec.Code, rec.Body.String())
		}
		var b config.Backends
		if err := json.NewDecoder(rec.Body).Decode(&b); err != nil {
			t.Fatal(err)
		}
		if len(b.GCP) != 1 || b.GCP[0].Name != "one" {
			t.Fatalf("unexpected GET body: %+v", b)
		}
	}

	// POST append GCP
	{
		rec := httptest.NewRecorder()
		body := []byte(`{"name":"two","project":"p2","zone":"z2"}`)
		req := httptest.NewRequest("POST", "/api/v1/config/backends/gcp", bytes.NewReader(body))
		req.SetPathValue("kind", "gcp")
		req.Header.Set("Content-Type", "application/json")
		auth(req)
		s.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST %d: %s", rec.Code, rec.Body.String())
		}
	}
	if s.opts.Config == nil {
		t.Fatal("after POST: s.opts.Config is nil")
	}
	if len(s.opts.Config.Backends.GCP) != 2 || s.opts.Config.Backends.GCP[1].Name != "two" {
		t.Fatalf("after POST in-memory: %+v", s.opts.Config.Backends.GCP)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backends.GCP) != 2 || cfg.Backends.GCP[1].Name != "two" {
		t.Fatalf("after POST: %+v", cfg.Backends.GCP)
	}

	// PUT replace index 0
	{
		rec := httptest.NewRecorder()
		body := []byte(`{"name":"oneb","project":"p1b","zone":"z1b"}`)
		req := httptest.NewRequest("PUT", "/api/v1/config/backends/gcp/0", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetPathValue("kind", "gcp")
		req.SetPathValue("index", "0")
		auth(req)
		s.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT %d: %s", rec.Code, rec.Body.String())
		}
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Backends.GCP[0].Name != "oneb" {
		t.Fatalf("after PUT index 0: %+v", cfg.Backends.GCP[0])
	}

	// DELETE index 1
	{
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("DELETE", "/api/v1/config/backends/gcp/1", nil)
		req.SetPathValue("kind", "gcp")
		req.SetPathValue("index", "1")
		auth(req)
		s.router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("DELETE %d: %s", rec.Code, rec.Body.String())
		}
	}
	cfg, err = config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backends.GCP) != 1 || cfg.Backends.GCP[0].Name != "oneb" {
		t.Fatalf("after DELETE: %+v", cfg.Backends.GCP)
	}

	bakPath := cfgPath + ".bak"
	b, err := os.ReadFile(bakPath)
	if err != nil {
		t.Fatalf("expected .bak: %v", err)
	}
	if !strings.Contains(string(b), "one") {
		t.Fatalf(".bak should contain prior yaml: %s", string(b))
	}
}

func TestConfigBackendsPutPathValues(t *testing.T) {
	// httptest.NewRequest may not populate PathValue in older Go; ensure mux hit works end-to-end.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "c.yaml")
	initialAWS := "version: 1\nbackends:\n  aws:\n    - name: a0\n      profile: p0\n      region: r0\n"
	if err := os.WriteFile(cfgPath, []byte(initialAWS), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "t", ConfigPath: cfgPath, SearchRegistry: searchrun.NewRegistry(all.Factories(all.Deps{}))})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		s.router.ServeHTTP(w, r)
	}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/config/backends/aws/0", strings.NewReader(`{"name":"x","profile":"p","region":"r"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer t")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT mux %d: %s", resp.StatusCode, string(body))
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Backends.AWS) != 1 || cfg.Backends.AWS[0].Name != "x" {
		t.Fatalf("loaded: %+v", cfg.Backends.AWS)
	}
}
