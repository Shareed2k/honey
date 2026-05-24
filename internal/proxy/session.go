package proxy

import (
	"context"
	"net/http"
	"time"

	"github.com/shareed2k/honey/internal/apps"
)

// Session represents an active application proxy session.
type Session struct {
	ID        string         `json:"id"`
	App       apps.AppConfig `json:"app"`
	LocalAddr string         `json:"local_addr"` // Only set if a local port is bound
	StartedAt time.Time      `json:"started_at"`
	ExpiresAt time.Time      `json:"expires_at"`
	PID       int            `json:"pid"`

	// Handler is used for HTTP apps that should be routed by the main webserver.
	// It is nil for TCP apps or when not running inside the webserver process.
	Handler http.Handler `json:"-"`

	// Stop handles tearing down the local listener and connections.
	Stop context.CancelFunc `json:"-"`
}
