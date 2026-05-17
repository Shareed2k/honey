package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerExposesHoneyMetrics(t *testing.T) {
	reg := NewRegistry("1.2.3", "abc")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	s := string(body)
	for _, want := range []string{
		"honey_build_info",
		`version="1.2.3"`,
		`commit="abc"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestMiddlewareRecordsRequest(t *testing.T) {
	reg := NewRegistry("dev", "none")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/meta", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := reg.Middleware(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	scrape := httptest.NewRecorder()
	reg.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	if !strings.Contains(body, `honey_http_requests_total{code="200",method="GET",route="GET /api/v1/meta"}`) {
		t.Errorf("expected http counter for GET /api/v1/meta, got:\n%s", body)
	}
}

func TestObserveSearch(t *testing.T) {
	reg := NewRegistry("dev", "none")
	reg.ObserveSearch(nil, 0, 42)
	scrape := httptest.NewRecorder()
	reg.Handler().ServeHTTP(scrape, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := scrape.Body.String()
	if !strings.Contains(body, `honey_search_requests_total{status="ok"}`) {
		t.Errorf("missing ok search counter: %s", body)
	}
	if !strings.Contains(body, "honey_search_records_bucket") {
		t.Errorf("missing search records histogram: %s", body)
	}
}
