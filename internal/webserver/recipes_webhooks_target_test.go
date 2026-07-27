package webserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/engine"
)

// TestHandleRecipeWebhook_targetResolutionReturns400 verifies the sync webhook
// path delegates host resolution to the runner (RunRequest.Target) and maps the
// runner's engine.ErrTargetResolution to a 400 (bad target) — replacing the old
// in-handler SearchHosts + errWebhookBadTarget dance. A fakeRunner returning the
// typed error stands in for a runner whose target search yielded no hosts.
func TestHandleRecipeWebhook_targetResolutionReturns400(t *testing.T) {
	sink := &captureSink{}
	s := newWebhookTestServer(t, webhookSyncCUE, "dynamic:localhost", sink,
		&fakeRunner{err: engine.ErrTargetResolution})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/myapp/deploy", strings.NewReader("{}"))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("target resolution failure must map to 400, got %d body=%s", w.Code, w.Body)
	}
}
