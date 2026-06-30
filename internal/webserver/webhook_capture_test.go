package webserver

import (
	"fmt"
	"sync"
	"testing"
)

func TestWebhookCaptureStore_RingTrim(t *testing.T) {
	t.Parallel()
	s := newWebhookCaptureStore()
	for i := 0; i < webhookCaptureMaxPerKey+5; i++ {
		s.Record("app", "hook", WebhookDelivery{ID: fmt.Sprintf("d%d", i)})
	}
	got := s.List("app", "hook", 0)
	if len(got) != webhookCaptureMaxPerKey {
		t.Fatalf("len = %d, want %d", len(got), webhookCaptureMaxPerKey)
	}
	// Newest first; oldest 5 (d0..d4) evicted.
	if got[0].ID != fmt.Sprintf("d%d", webhookCaptureMaxPerKey+4) {
		t.Errorf("newest = %s", got[0].ID)
	}
	if got[len(got)-1].ID != "d5" {
		t.Errorf("oldest retained = %s, want d5", got[len(got)-1].ID)
	}
}

func TestWebhookCaptureStore_PerKeyIsolation(t *testing.T) {
	t.Parallel()
	s := newWebhookCaptureStore()
	s.Record("a", "h", WebhookDelivery{ID: "a1"})
	s.Record("b", "h", WebhookDelivery{ID: "b1"})

	if a := s.List("a", "h", 0); len(a) != 1 || a[0].ID != "a1" {
		t.Errorf("key a = %+v", a)
	}
	if b := s.List("b", "h", 0); len(b) != 1 || b[0].ID != "b1" {
		t.Errorf("key b = %+v", b)
	}
	if e := s.List("missing", "h", 0); len(e) != 0 {
		t.Errorf("missing key returned %d entries", len(e))
	}
}

func TestWebhookCaptureStore_Limit(t *testing.T) {
	t.Parallel()
	s := newWebhookCaptureStore()
	for i := 0; i < 10; i++ {
		s.Record("a", "h", WebhookDelivery{ID: fmt.Sprintf("d%d", i)})
	}
	if got := s.List("a", "h", 3); len(got) != 3 {
		t.Fatalf("limit 3 returned %d", len(got))
	}
}

func TestWebhookCaptureStore_BodyTruncated(t *testing.T) {
	t.Parallel()
	s := newWebhookCaptureStore()
	s.Record("a", "h", WebhookDelivery{Body: string(make([]byte, webhookCaptureMaxBodyLen+100))})
	if got := s.List("a", "h", 1); len(got[0].Body) != webhookCaptureMaxBodyLen {
		t.Fatalf("body len = %d, want %d", len(got[0].Body), webhookCaptureMaxBodyLen)
	}
}

// Run with -race to verify the compound get-append-trim-put is safe.
func TestWebhookCaptureStore_ConcurrentRecord(t *testing.T) {
	t.Parallel()
	s := newWebhookCaptureStore()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.Record("a", "h", WebhookDelivery{ID: fmt.Sprintf("d%d", i)})
			_ = s.List("a", "h", 5)
		}(i)
	}
	wg.Wait()
	if got := s.List("a", "h", 0); len(got) != webhookCaptureMaxPerKey {
		t.Fatalf("len = %d, want %d", len(got), webhookCaptureMaxPerKey)
	}
}
