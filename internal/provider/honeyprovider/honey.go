// Package honeyprovider implements the remote honey backend integration.
package honeyprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
)

// Honey implements the honey backend provider.
type Honey struct {
	Name     string
	URL      string
	Token    string
	Insecure bool
	// MTLS routes this backend over the device mTLS client credential (registered
	// via devmtls) instead of the bearer token. ServerCA optionally pins the
	// gateway server cert.
	MTLS     bool
	ServerCA string
	// Mesh routes this backend through the local libp2p mesh client
	// (internal/meshnet) instead of the normal network path. MeshAddr is the
	// libp2p multiaddr to dial when Mesh is true.
	Mesh     bool
	MeshAddr string

	clientOnce sync.Once
	client     *http.Client
	clientErr  error
}

// ID returns the backend identifier.
func (h *Honey) ID() string { return "honey" }

// BackendName returns the optional config label.
func (h *Honey) BackendName() string { return strings.TrimSpace(h.Name) }

// CacheIdentity scopes cache entries per backend.
func (h *Honey) CacheIdentity() string {
	return h.URL
}

var _ hosts.Backend = (*Honey)(nil)

type searchResponse struct {
	Records []hosts.Record `json:"records"`
}

// Search queries the remote honey server for hosts matching the query.
func (h *Honey) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	// Strip tokens that name this honey proxy itself before forwarding to the server.
	// The server only knows its own sub-backends (e.g. "gcp-prod"), not our local config name.
	ownName := strings.ToLower(strings.TrimSpace(h.BackendName()))
	var serverBackends []string
	for _, b := range q.Backends {
		lb := strings.ToLower(strings.TrimSpace(b))
		if lb == "honey" || lb == ownName || (ownName != "" && lb == "honey:"+ownName) {
			continue
		}
		serverBackends = append(serverBackends, b)
	}

	reqBody := hostapi.SearchHostsInput{
		Name:      q.NameSubstring,
		NameRegex: q.NameRegex,
		Providers: strings.Join(q.Providers, ","),
		Backends:  strings.Join(serverBackends, ","),
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	searchURL := strings.TrimRight(h.URL, "/") + "/api/v1/search"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, searchURL, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// mTLS-managed backends authenticate by client cert; token-based ones by bearer.
	if !h.MTLS && h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}

	h.clientOnce.Do(func() {
		tr, err := buildTransport(trustConfig{
			insecure: h.Insecure,
			mtls:     h.MTLS,
			serverCA: h.ServerCA,
			mesh:     h.Mesh,
			meshAddr: h.MeshAddr,
		})
		if err != nil {
			h.clientErr = fmt.Errorf("honey mTLS: %w", err)
			return
		}
		h.client = &http.Client{Transport: tr}
	})
	if h.clientErr != nil {
		return nil, h.clientErr
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var searchResp searchResponse
	if err := json.NewDecoder(resp.Body).Decode(&searchResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	for i := range searchResp.Records {
		if searchResp.Records[i].Meta == nil {
			searchResp.Records[i].Meta = make(map[string]string)
		}
		searchResp.Records[i].Meta["honey_upstream_backend"] = h.BackendName()
	}

	return searchResp.Records, nil
}
