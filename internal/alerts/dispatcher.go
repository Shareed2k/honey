// Package alerts dispatches anomaly notifications with TTL-cache deduplication.
package alerts

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"github.com/nikoksr/notify"
)

// Dispatcher sends anomaly notifications with TTL-cache deduplication.
// A (source, reason-category) pair is suppressed for SuppressWindow after first delivery.
type Dispatcher struct {
	notifier       *notify.Notify
	suppressWindow time.Duration
	seen           *ttlcache.Cache[string, struct{}]
}

// New creates a Dispatcher. suppressWindow=0 disables dedup (every anomaly notified).
func New(n *notify.Notify, suppressWindow time.Duration) *Dispatcher {
	c := ttlcache.New[string, struct{}](
		ttlcache.WithTTL[string, struct{}](suppressWindow), //nolint:staticcheck
	)
	go c.Start()
	return &Dispatcher{notifier: n, suppressWindow: suppressWindow, seen: c}
}

// Close stops the background TTL goroutine.
func (d *Dispatcher) Close() { d.seen.Stop() }

// Dispatch sends an alert unless the same (source, reason-category) was notified
// within the suppress window.
func (d *Dispatcher) Dispatch(ctx context.Context, source string, score float64, reason, line string) {
	if d.suppressWindow > 0 {
		fp := fingerprint(source, reason)
		if d.seen.Has(fp) {
			return
		}
		d.seen.Set(fp, struct{}{}, ttlcache.DefaultTTL)
	}
	subject := fmt.Sprintf("[honey] anomaly on %s", strings.TrimSpace(source))
	body := fmt.Sprintf("Source: %s\nScore:  %.2f\nReason: %s\nTime:   %s\n\n%s",
		strings.TrimSpace(source), score, reason, time.Now().UTC().Format(time.RFC3339), line)
	_ = d.notifier.Send(ctx, subject, body)
}

// fingerprint produces a short stable key from source + reason-category.
func fingerprint(source, reason string) string {
	cat := strings.SplitN(reason, ":", 2)[0]
	h := sha256.Sum256([]byte(source + ":" + cat))
	return fmt.Sprintf("%x", h[:6])
}
