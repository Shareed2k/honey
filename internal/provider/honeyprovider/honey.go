// Package honeyprovider implements the remote honey backend integration.
package honeyprovider

import (
	"bytes"
	"context"
	"crypto/tls"
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

	clientOnce sync.Once
	client     *http.Client
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
	reqBody := hostapi.SearchHostsInput{
		Name:      q.NameSubstring,
		NameRegex: q.NameRegex,
		Providers: strings.Join(q.Providers, ","),
		Backends:  strings.Join(q.Backends, ","),
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
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}

	h.clientOnce.Do(func() {
		var tr *http.Transport
		if t, ok := http.DefaultTransport.(*http.Transport); ok {
			tr = t.Clone()
		} else {
			tr = &http.Transport{}
		}
		if h.Insecure {
			if tr.TLSClientConfig == nil {
				tr.TLSClientConfig = &tls.Config{}
			}
			tr.TLSClientConfig.InsecureSkipVerify = true
		}
		h.client = &http.Client{Transport: tr}
	})

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

	return searchResp.Records, nil
}
