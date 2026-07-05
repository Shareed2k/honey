//go:build integration

package integration

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/webserver"
)

// TestE2E_WebhookRateLimit verifies that the unauthenticated webhook endpoint
// enforces its per-app-name token bucket: once the burst is consumed every
// subsequent call returns 429 Too Many Requests.
func TestE2E_WebhookRateLimit(t *testing.T) {
	t.Parallel()

	// burst=1, 1 req/s: first request takes the only token; all subsequent
	// requests within the same second must be rejected.
	base := newTestServer(t, webserver.Options{
		WebhookRatePerSecond: 1,
		WebhookBurst:         1,
	})

	client := &http.Client{Timeout: 5 * time.Second}
	url := base + "/api/v1/webhooks/myapp/myhook"

	// First request: consumes the single token. The app is not configured so
	// the handler returns an error after the rate-limit gate — that is fine,
	// we only care about the 429 on the second call.
	first, err := client.Post(url, "application/json", nil)
	require.NoError(t, err)
	first.Body.Close()
	require.NotEqual(t, http.StatusTooManyRequests, first.StatusCode,
		"first request should consume token, not be rate-limited")

	// Second request: token bucket empty → must be 429.
	second, err := client.Post(url, "application/json", nil)
	require.NoError(t, err)
	second.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, second.StatusCode,
		"second request within same second should be rate-limited")
}

// TestE2E_WebhookRateLimit_PerApp verifies isolation: exhausting "app-a"'s
// bucket does not affect "app-b".
func TestE2E_WebhookRateLimit_PerApp(t *testing.T) {
	t.Parallel()

	base := newTestServer(t, webserver.Options{
		WebhookRatePerSecond: 1,
		WebhookBurst:         1,
	})

	client := &http.Client{Timeout: 5 * time.Second}

	// Exhaust app-a's bucket.
	respA1, err := client.Post(base+"/api/v1/webhooks/app-a/hook", "application/json", nil)
	require.NoError(t, err)
	respA1.Body.Close()

	respA2, err := client.Post(base+"/api/v1/webhooks/app-a/hook", "application/json", nil)
	require.NoError(t, err)
	respA2.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, respA2.StatusCode, "app-a second call should be rate-limited")

	// app-b still has its full bucket.
	respB, err := client.Post(base+"/api/v1/webhooks/app-b/hook", "application/json", nil)
	require.NoError(t, err)
	respB.Body.Close()
	assert.NotEqual(t, http.StatusTooManyRequests, respB.StatusCode, "app-b should not be rate-limited by app-a exhaustion")
}
