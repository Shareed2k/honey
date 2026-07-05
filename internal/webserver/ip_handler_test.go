package webserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

type mockBackend struct {
	id      string
	records []hosts.Record
}

func (m mockBackend) ID() string            { return m.id }
func (m mockBackend) BackendName() string   { return m.id }
func (m mockBackend) CacheIdentity() string { return m.id }
func (m mockBackend) Search(_ context.Context, q hosts.Query) ([]hosts.Record, error) {
	if q.NameSubstring != "" {
		var res []hosts.Record
		for _, rec := range m.records {
			if strings.Contains(rec.Name, q.NameSubstring) {
				res = append(res, rec)
			}
		}
		return res, nil
	}
	return m.records, nil
}

type mockFactory struct {
	backend hosts.Backend
}

func (m mockFactory) FromConfig(_ searchrun.ProviderOverrides) []hosts.Backend {
	return []hosts.Backend{m.backend}
}

func (m mockFactory) Default(_ searchrun.ProviderOverrides) hosts.Backend {
	return m.backend
}
func (m mockFactory) BackendRows() []config.BackendRow { return nil }

func TestHandleIP(t *testing.T) {
	mock1 := mockFactory{backend: mockBackend{
		id: "mock1",
		records: []hosts.Record{
			{Name: "host1", PrimaryIP: "1.2.3.4", Provider: "mock1", Zone: "us-east"},
		},
	}}
	mock2 := mockFactory{backend: mockBackend{
		id: "mock2",
		records: []hosts.Record{
			{Name: "host2", PrimaryIP: "5.6.7.8", Provider: "mock2", Zone: "us-west", Meta: map[string]string{"foo": "bar"}},
			{Name: "host3", PrimaryIP: "9.10.11.12", Provider: "mock2", Zone: "eu-central"},
		},
	}}

	reg := searchrun.NewRegistry([]searchrun.ProviderFactory{mock1, mock2})

	srv := &Server{
		opts: Options{
			SearchRegistry: reg,
		},
	}
	r := chi.NewRouter()
	r.Get("/ip", srv.handleIP)
	r.Get("/ip/", srv.handleIP)
	r.Get("/ip.json", srv.handleIP)
	r.Get("/ip/backend/{name}", srv.handleIP)
	r.Get("/ip/backend/{name}/", srv.handleIP)
	r.Get("/ip/backend/{name}.json", srv.handleIP)
	r.Get("/ip/provider/{name}", srv.handleIP)
	r.Get("/ip/provider/{name}/", srv.handleIP)
	r.Get("/ip/provider/{name}.json", srv.handleIP)

	tests := []struct {
		name           string
		path           string
		accept         string
		expectedStatus int
		expectedType   string
		checkBody      func(t *testing.T, body string)
	}{
		{
			name:           "missing params",
			path:           "/ip",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "single match text",
			path:           "/ip?name=host1",
			expectedStatus: http.StatusOK,
			expectedType:   "text/plain",
			checkBody: func(t *testing.T, body string) {
				assert.Equal(t, "1.2.3.4\n", body)
			},
		},
		{
			name:           "multiple match text",
			path:           "/ip/provider/mock2",
			expectedStatus: http.StatusOK,
			expectedType:   "text/plain",
			checkBody: func(t *testing.T, body string) {
				assert.Contains(t, body, "PRIMARY_IP")
				assert.Contains(t, body, "5.6.7.8")
				assert.Contains(t, body, "9.10.11.12")
			},
		},
		{
			name:           "trailing slash backend",
			path:           "/ip/backend/mock1/",
			expectedStatus: http.StatusOK,
			expectedType:   "text/plain",
			checkBody: func(t *testing.T, body string) {
				assert.Equal(t, "1.2.3.4\n", body)
			},
		},
		{
			name:           "json output",
			path:           "/ip/provider/mock2.json",
			expectedStatus: http.StatusOK,
			expectedType:   "application/json",
			checkBody: func(t *testing.T, body string) {
				var recs []hosts.Record
				err := json.Unmarshal([]byte(body), &recs)
				assert.NoError(t, err)
				assert.Len(t, recs, 2)
				assert.Equal(t, "5.6.7.8", recs[0].PrimaryIP)
			},
		},
		{
			name:           "html output",
			path:           "/ip?name=host2",
			accept:         "text/html",
			expectedStatus: http.StatusOK,
			expectedType:   "text/html; charset=utf-8",
			checkBody: func(t *testing.T, body string) {
				assert.Contains(t, body, "<h2>Resolved Hosts</h2>")
				assert.Contains(t, body, "5.6.7.8")
				assert.Contains(t, body, "<td>foo</td><td>bar</td>")
			},
		},
		{
			name:           "not found",
			path:           "/ip?name=nonexistent",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.path, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedType != "" {
				assert.Equal(t, tt.expectedType, w.Header().Get("Content-Type"))
			}
			if tt.checkBody != nil {
				tt.checkBody(t, w.Body.String())
			}
		})
	}
}
