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
	s.retentionState.mu.Lock()
	s.retentionState.retention = maxAge
	s.retentionState.retentionText = text
	s.retentionState.mu.Unlock()

	run := func() {
		res, err := recordings.PurgeExpired(s.opts.RecordDir, maxAge)
		s.retentionState.mu.Lock()
		s.retentionState.lastPurgeAt = time.Now().UTC()
		s.retentionState.lastDeleted = res.Deleted
		s.retentionState.mu.Unlock()
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
