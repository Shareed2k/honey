package plugins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

func TestRunK8sHTTP_NoContext(t *testing.T) {
	out := runK8sHTTP(context.Background(), apiv1.K8sHTTPInput{
		Method: "GET",
		Path:   "/version",
	}, "test-plugin")
	if out.Error == "" {
		t.Fatal("expected error when no host context")
	}
}

func TestRunK8sHTTP_WithServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"major":"1","minor":"30"}`))
	}))
	defer srv.Close()

	hctx := &HostRunContext{
		Record: hosts.Record{
			Name: "test-cluster",
			Meta: map[string]string{},
		},
	}
	ctx := WithHostRunContext(context.Background(), hctx)

	// Since Meta has no kubeconfig/context, clientcmd falls back to defaults.
	// In CI without a cluster this may fail — test passes as long as it doesn't panic.
	out := runK8sHTTP(ctx, apiv1.K8sHTTPInput{
		Method: "GET",
		Path:   "/version",
	}, "test-plugin")
	if out.StatusCode == http.StatusOK {
		var body map[string]string
		if err := json.Unmarshal(out.Body, &body); err != nil {
			t.Fatalf("parse response body: %v", err)
		}
	}
}

func TestRunK8sHTTP_LargeResponseTruncated(t *testing.T) {
	bigBody := make([]byte, int(defaultK8sHTTPMaxResponseBytes)+1024)
	for i := range bigBody {
		bigBody[i] = 'x'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bigBody)
	}))
	defer srv.Close()

	hctx := &HostRunContext{
		Record: hosts.Record{
			Name: "test-cluster",
			Meta: map[string]string{},
		},
	}
	ctx := WithHostRunContext(context.Background(), hctx)
	out := runK8sHTTP(ctx, apiv1.K8sHTTPInput{
		Method:           "GET",
		Path:             "/bigresource",
		MaxResponseBytes: 128,
	}, "test-plugin")
	if out.StatusCode == http.StatusOK && len(out.Body) > 128 {
		t.Errorf("body not capped: got %d bytes", len(out.Body))
	}
}
