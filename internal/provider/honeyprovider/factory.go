package honeyprovider

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
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
	client := &http.Client{Timeout: 2 * time.Second}

	for _, b := range f.cfg.HoneyBackends() {
		// 1. Add the Honey backend itself
		rows = append(rows, config.BackendRow{
			Kind: "honey",
			Name: b.Name,
			Hint: b.URL,
		})

		// 2. Fetch remote backends
		fetchURL := strings.TrimRight(b.URL, "/") + "/api/v1/config/backends"
		req, err := http.NewRequest(http.MethodGet, fetchURL, nil)
		if err != nil {
			continue
		}
		if b.Token != "" {
			req.Header.Set("Authorization", "Bearer "+b.Token)
		}

		// Handle insecure TLS
		if b.Insecure {
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // #nosec G402
			}
			client.Transport = tr
		} else {
			client.Transport = nil // Use default
		}

		resp, err := client.Do(req)
		if err != nil {
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var out hostapi.ListBackendsOutput
			if err := json.NewDecoder(resp.Body).Decode(&out); err == nil {
				// Append fetched backends
				rows = append(rows, out.Backends...)
			}
		}
		resp.Body.Close()
	}
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
