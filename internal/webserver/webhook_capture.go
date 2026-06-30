package webserver

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"

	"github.com/shareed2k/honey/internal/engine"
)

// newDeliveryID returns a short random hex id for a captured delivery.
//
//nolint:unused // kept for future use
func newDeliveryID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// WebhookDelivery is a captured webhook invocation — a live delivery, a debug
// test-send, or a dry-run preview. It is what the web UI's webhook-debugging
// panel displays.
type WebhookDelivery struct {
	ID             string                  `json:"id"`
	Source         string                  `json:"source"` // live | test | dry_run
	ReceivedAt     time.Time               `json:"received_at"`
	RemoteAddr     string                  `json:"remote_addr,omitempty"`
	ContentType    string                  `json:"content_type,omitempty"`
	Body           string                  `json:"body"`
	AuthOK         bool                    `json:"auth_ok"`
	Extracted      map[string]string       `json:"extracted,omitempty"`
	Actor          string                  `json:"actor,omitempty"`
	IdempotencyKey string                  `json:"idempotency_key,omitempty"`
	Async          bool                    `json:"async"`
	Outcome        string                  `json:"outcome"` // executed | queued | dry_run | unauthorized | duplicate | error | completed | failed
	ExecID         string                  `json:"exec_id,omitempty"`
	Error          string                  `json:"error,omitempty"`
	Results        []engine.HostExecResult `json:"results,omitempty"`
}

const (
	webhookCaptureMaxKeys    = 512      // distinct (app,webhook) pairs retained
	webhookCaptureMaxPerKey  = 20       // deliveries kept per pair
	webhookCaptureMaxBodyLen = 64 << 10 // cap captured body at 64 KiB
)

// webhookCaptureStore keeps the most-recent deliveries per (app, webhook) in
// memory. The outer LRU bounds the number of distinct webhooks tracked
// (evicting least-recently-used pairs); each entry holds the newest
// webhookCaptureMaxPerKey deliveries. State is in-memory only and lost on restart.
type webhookCaptureStore struct {
	mu        sync.Mutex
	cache     *lru.Cache[string, []WebhookDelivery]
	maxPerKey int
}

func newWebhookCaptureStore() *webhookCaptureStore {
	c, _ := lru.New[string, []WebhookDelivery](webhookCaptureMaxKeys) // err only when size <= 0
	return &webhookCaptureStore{cache: c, maxPerKey: webhookCaptureMaxPerKey}
}

func webhookCaptureKey(app, webhook string) string { return app + "\x00" + webhook }

// Record appends d for (app, webhook), truncating the body and dropping the
// oldest delivery once the per-key cap is exceeded.
func (s *webhookCaptureStore) Record(app, webhook string, d WebhookDelivery) {
	if len(d.Body) > webhookCaptureMaxBodyLen {
		d.Body = d.Body[:webhookCaptureMaxBodyLen]
	}
	key := webhookCaptureKey(app, webhook)

	s.mu.Lock()
	defer s.mu.Unlock()
	prev, _ := s.cache.Get(key)
	// Copy into a fresh, exactly-sized slice so trimming cannot retain an
	// ever-growing backing array across many Record calls.
	next := make([]WebhookDelivery, 0, min(len(prev)+1, s.maxPerKey))
	if len(prev) >= s.maxPerKey {
		next = append(next, prev[len(prev)-s.maxPerKey+1:]...)
	} else {
		next = append(next, prev...)
	}
	next = append(next, d)
	s.cache.Add(key, next)
}

// List returns up to limit most-recent deliveries (newest first) for (app, webhook).
// A limit <= 0 returns all retained deliveries.
func (s *webhookCaptureStore) List(app, webhook string, limit int) []WebhookDelivery {
	key := webhookCaptureKey(app, webhook)

	s.mu.Lock()
	defer s.mu.Unlock()
	stored, ok := s.cache.Get(key)
	if !ok || len(stored) == 0 {
		return []WebhookDelivery{}
	}
	out := make([]WebhookDelivery, 0, len(stored))
	for i := len(stored) - 1; i >= 0; i-- {
		out = append(out, stored[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
