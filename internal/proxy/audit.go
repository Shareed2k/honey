// Package proxy manages background connections, tunnels, and active sessions for applications.
package proxy

import (
	"strings"
	"time"

	"go.uber.org/zap"
)

// AuditEvent represents a proxy lifecycle event.
type AuditEvent struct {
	Event     string    `json:"event"`
	App       string    `json:"app"`
	Target    string    `json:"target"`
	Upstream  string    `json:"upstream"`
	LocalAddr string    `json:"local_addr"`
	Time      time.Time `json:"time"`
	Error     string    `json:"error,omitempty"`
}

// Logger helps record proxy audit events safely.
type Logger struct {
	zap *zap.Logger
}

// NewLogger creates a new audit logger to record proxy events.
func NewLogger(z *zap.Logger) *Logger {
	return &Logger{zap: z}
}

func (l *Logger) emit(event string, s *Session, err error) {
	if l.zap == nil {
		return
	}

	fields := []zap.Field{
		zap.String("event", event),
		zap.String("app", s.App.Name),
		zap.String("target", s.App.Target),
		zap.String("upstream", redactURL(s.App.Upstream)),
		zap.String("local_addr", s.LocalAddr),
	}

	if err != nil {
		fields = append(fields, zap.Error(err))
		l.zap.Info("Proxy event", fields...)
	} else {
		l.zap.Info("Proxy event", fields...)
	}
}

// Started emits an event when a proxy session starts.
func (l *Logger) Started(s *Session) {
	l.emit("proxy_started", s, nil)
}

// Stopped emits an event when a proxy session is gracefully stopped.
func (l *Logger) Stopped(s *Session) {
	l.emit("proxy_stopped", s, nil)
}

// Expired emits an event when a proxy session reaches its TTL.
func (l *Logger) Expired(s *Session) {
	l.emit("proxy_expired", s, nil)
}

// Failed emits an event when a proxy session encounters an error.
func (l *Logger) Failed(s *Session, err error) {
	l.emit("proxy_failed", s, err)
}

// redactURL masks credentials in a URL string (e.g. scheme://xxxxx@host).
func redactURL(raw string) string {
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd == -1 {
		return raw
	}
	atIndex := strings.LastIndex(raw, "@")
	if atIndex > schemeEnd+3 {
		return raw[:schemeEnd+3] + "xxxxx" + raw[atIndex:]
	}
	return raw
}
