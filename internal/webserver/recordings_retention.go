package webserver

import (
	"context"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/recordings"
)

const recordingRetentionInterval = time.Hour

type recordingRetentionState struct {
	mu            sync.Mutex
	lastPurgeAt   time.Time
	lastDeleted   int
	retention     time.Duration
	retentionText string
}

func (r *recordingRetentionState) setRetention(maxAge time.Duration, text string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retention = maxAge
	r.retentionText = text
}

func (r *recordingRetentionState) recordPurge(deleted int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastPurgeAt = time.Now().UTC()
	r.lastDeleted = deleted
}

// lastPurge returns the most recent purge time and whether a purge has run.
func (r *recordingRetentionState) lastPurge() (time.Time, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastPurgeAt, !r.lastPurgeAt.IsZero()
}

func (s *Server) recordingRetentionMaxAge() (time.Duration, string) {
	if s.opts.Config == nil {
		return 0, ""
	}
	d, ok, err := s.opts.Config.Defaults.DefaultsRecordRetention()
	if err != nil {
		zap.L().Warn("invalid defaults.record_retention", zap.Error(err))
		return 0, ""
	}
	if !ok {
		return 0, ""
	}
	return d, strings.TrimSpace(s.opts.Config.Defaults.RecordRetention)
}

func (s *Server) startRecordingRetention(ctx context.Context) {
	maxAge, text := s.recordingRetentionMaxAge()
	if maxAge <= 0 || strings.TrimSpace(s.opts.RecordDir) == "" {
		return
	}
	s.retentionState.setRetention(maxAge, text)

	run := func() {
		res, err := recordings.PurgeExpired(s.opts.RecordDir, maxAge)
		s.retentionState.recordPurge(res.Deleted)
		if err != nil {
			zap.L().Warn("recording retention sweep failed", zap.Error(err))
		}
	}
	run()
	go func() {
		ticker := time.NewTicker(recordingRetentionInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				run()
			}
		}
	}()
}
