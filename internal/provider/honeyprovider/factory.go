package honeyprovider

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
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
		out = append(out, &Honey{
			Name:     b.Name,
			URL:      b.URL,
			Token:    b.Token,
			Insecure: b.Insecure,
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

	// 1. Add the Honey backends themselves
	for _, b := range backends {
		rows = append(rows, config.BackendRow{
			Kind: "honey",
			Name: b.Name,
			Hint: b.URL,
		})
	}

	// 2. Fetch remote backends concurrently
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Use DisableKeepAlives to prevent connection leaks for these short-lived fetches.
	defaultClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
	insecureClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, // #nosec G402
		},
	}

	for _, b := range backends {
		b := b // capture loop variable
		wg.Add(1)

		go func() {
			defer wg.Done()

			fetchURL := strings.TrimRight(b.URL, "/") + "/api/v1/backends"
			req, err := http.NewRequest(http.MethodGet, fetchURL, nil)
			if err != nil {
				return
			}
			if b.Token != "" {
				req.Header.Set("Authorization", "Bearer "+b.Token)
			}

			client := defaultClient
			if b.Insecure {
				client = insecureClient
			}

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

var (
	_ searchrun.ProviderFactory       = honeyFactory{}
	_ searchrun.BackendConfigRegistry = honeyFactory{}
)
