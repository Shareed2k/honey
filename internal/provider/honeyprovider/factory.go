package honeyprovider

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/devmtls"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// ConfigProvider defines the configuration dependency required by this provider.
type ConfigProvider interface {
	HoneyBackends() []config.HoneyBackend
	HoneyBackendSlicePtr() *[]config.HoneyBackend
	SetHoneyBackends([]config.HoneyBackend)
}

// NewFactory returns a new factory for this provider.
func NewFactory(cfg ConfigProvider) searchrun.ProviderFactory {
	searchrun.RegisterCRUD(honeyCRUD{cfg: cfg})
	return honeyFactory{cfg: cfg}
}

type honeyFactory struct {
	cfg ConfigProvider
}

func (f honeyFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	if len(f.cfg.HoneyBackends()) == 0 {
		return nil
	}
	var out []hosts.Backend
	for _, b := range f.cfg.HoneyBackends() {
		if b.MTLS && !devmtls.Registered() {
			continue // mTLS-managed but no device credential registered yet
		}
		out = append(out, &Honey{
			Name:     b.Name,
			URL:      b.URL,
			Token:    b.Token,
			Insecure: b.Insecure,
			MTLS:     b.MTLS,
			ServerCA: b.ServerCA,
			// No MTLS-style skip for Mesh: this process owns the mesh dial itself
			// (unlike an MTLS-managed credential owned by the mobile app), so an
			// unready/failed mesh must surface as a normal per-call network error
			// from Search(), not exclude the backend from the list entirely.
			Mesh:     b.Mesh,
			MeshAddr: b.MeshAddr,
		})
	}
	return out
}

func (f honeyFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return &Honey{}
}

func (f honeyFactory) BackendRows() []config.BackendRow {
	var rows []config.BackendRow
	backends := f.cfg.HoneyBackends()

	if len(backends) == 0 {
		return nil
	}

	// 1. Add the Honey backends themselves (skip mTLS-managed ones only when no
	// device credential is registered to reach them).
	for _, b := range backends {
		if b.MTLS && !devmtls.Registered() {
			continue
		}
		rows = append(rows, config.BackendRow{
			Kind: "honey",
			Name: b.Name,
			Hint: b.URL,
		})
	}

	// 2. Fetch remote backends concurrently
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, b := range backends {
		b := b // capture loop variable
		if b.MTLS && !devmtls.Registered() {
			continue // mTLS-managed but no device credential registered yet
		}
		wg.Add(1)

		go func() {
			defer wg.Done()

			fetchURL := strings.TrimRight(b.URL, "/") + "/api/v1/backends"
			req, err := http.NewRequest(http.MethodGet, fetchURL, nil)
			if err != nil {
				return
			}
			if !b.MTLS && b.Token != "" {
				req.Header.Set("Authorization", "Bearer "+b.Token)
			}

			tr, terr := buildTransport(trustConfig{
				insecure: b.Insecure,
				mtls:     b.MTLS,
				serverCA: b.ServerCA,
				mesh:     b.Mesh,
				meshAddr: b.MeshAddr,
			})
			if terr != nil {
				return
			}
			// Use DisableKeepAlives to prevent connection leaks for this short-lived fetch.
			tr.DisableKeepAlives = true
			client := &http.Client{Timeout: 2 * time.Second, Transport: tr}

			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				var out hostapi.ListBackendsOutput
				if err := json.NewDecoder(resp.Body).Decode(&out); err == nil {
					mu.Lock()
					rows = append(rows, out.Backends...)
					mu.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	return rows
}

func (f honeyFactory) BackendKind() string {
	return "honey"
}

func (f honeyFactory) BackendSlicePtr() any {
	return f.cfg.HoneyBackendSlicePtr()
}

// HandlesRecord claims a record only when THIS node can actually proxy it: the
// record carries an upstream-routing tag AND a honey backend with that name is
// configured here. On the client (which has the backend) this claims the record
// and it is proxied; on the upstream server (no such backend) it declines, so the
// record falls through to the native factory (docker/k8s) and is executed locally.
// (Claiming merely on the tag being present would dead-end the record at the SSH
// fallback on the server, since ExecutorFor would then return nil.)
func (f honeyFactory) HandlesRecord(r hosts.Record) bool {
	name := r.Meta["honey_upstream_backend"]
	if name == "" {
		return false
	}
	for _, b := range f.cfg.HoneyBackends() {
		if b.Name == name {
			return true
		}
	}
	return false
}

func (f honeyFactory) ExecutorFor(r hosts.Record, _ hostexec.Registry) hostexec.Executor {
	// Find the configured honey backend matching the record's upstream source.
	upstreamName := r.Meta["honey_upstream_backend"]
	for _, b := range f.cfg.HoneyBackends() {
		if b.Name == upstreamName {
			if b.MTLS && !devmtls.Registered() {
				return nil // no device credential to reach the mTLS gateway
			}
			return &Executor{
				URL:      b.URL,
				Token:    b.Token,
				Insecure: b.Insecure,
				MTLS:     b.MTLS,
				ServerCA: b.ServerCA,
				Mesh:     b.Mesh,
				MeshAddr: b.MeshAddr,
			}
		}
	}
	return nil
}

var (
	_ searchrun.ProviderFactory       = honeyFactory{}
	_ searchrun.BackendConfigRegistry = honeyFactory{}
	_ searchrun.ExecutorProvider      = honeyFactory{}
)
