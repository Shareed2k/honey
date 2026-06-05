package anomaly

import (
	"context"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

// StorageRecord holds the structured metadata of an anomaly log record.
type StorageRecord struct {
	Timestamp string  `json:"ts"`
	Source    string  `json:"source"`
	Line      string  `json:"line"`
	Score     float64 `json:"score"`
	Reason    string  `json:"reason"`
	Anomaly   bool    `json:"anomaly"`
}

// Storage defines the interface for persisting scored/anomalous log records.
type Storage interface {
	Write(ctx context.Context, rec StorageRecord) error
	WriteBatch(ctx context.Context, records []StorageRecord) error
	Close() error
}

// BatchStorage wraps any Storage engine and performs in-memory LRU deduplication
// and buffered batch-writing inside a background worker goroutine.
type BatchStorage struct {
	inner     Storage
	batchSize int
	timeout   time.Duration
	ch        chan StorageRecord
	closeCh   chan struct{}
	wg        sync.WaitGroup
	written   *lru.Cache[string, bool] // Atomic thread-safe uniqueness filter
}

// NewBatchStorage instantiates a BatchStorage wrapper with a 10,000-entry LRU deduplicator.
func NewBatchStorage(inner Storage, batchSize int, timeout time.Duration) *BatchStorage {
	c, _ := lru.New[string, bool](10_000)
	b := &BatchStorage{
		inner:     inner,
		batchSize: batchSize,
		timeout:   timeout,
		ch:        make(chan StorageRecord, batchSize*2),
		closeCh:   make(chan struct{}),
		written:   c,
	}
	b.wg.Add(1)
	go b.worker()
	return b
}

// Write checks-and-adds the log's normalized template to the LRU cache atomically.
// If the template signature is already written, it is skipped cleanly.
func (b *BatchStorage) Write(ctx context.Context, rec StorageRecord) error {
	norm := Normalize(rec.Line)
	ok, _ := b.written.ContainsOrAdd(norm, true)
	if ok {
		return nil // Deduplicated successfully!
	}

	select {
	case b.ch <- rec:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WriteBatch bypasses the queue and writes the batch directly to the inner storage.
func (b *BatchStorage) WriteBatch(ctx context.Context, records []StorageRecord) error {
	return b.inner.WriteBatch(ctx, records)
}

func (b *BatchStorage) worker() {
	defer b.wg.Done()
	ticker := time.NewTicker(b.timeout)
	defer ticker.Stop()

	var buf []StorageRecord
	flush := func() {
		if len(buf) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = b.inner.WriteBatch(ctx, buf)
		cancel()
		buf = nil
	}

	for {
		select {
		case rec := <-b.ch:
			buf = append(buf, rec)
			if len(buf) >= b.batchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-b.closeCh:
			// Drain remaining records on shutdown
			for {
				select {
				case rec := <-b.ch:
					buf = append(buf, rec)
				default:
					flush()
					return
				}
			}
		}
	}
}

// Close flushes the remaining buffer and closes the inner storage engine.
func (b *BatchStorage) Close() error {
	close(b.closeCh)
	b.wg.Wait()
	return b.inner.Close()
}
