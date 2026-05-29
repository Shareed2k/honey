package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/apps"
)

// StartTCPProxy starts a raw TCP proxy bound to 127.0.0.1.
func StartTCPProxy(ctx context.Context, app apps.AppConfig, dialer Dialer, sessionID string, closer io.Closer) (*Session, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", app.LocalPort))
	if err != nil {
		return nil, fmt.Errorf("failed to start tcp proxy: %w", err)
	}

	sessionCtx, cancel := context.WithCancel(ctx)

	go func() {
		<-sessionCtx.Done()
		_ = ln.Close()
	}()

	go func() {
		for {
			clientConn, err := ln.Accept()
			if err != nil {
				return // Listener closed or error
			}

			go func() {
				upstreamAddr := tcpDialAddress(app)
				upstreamConn, err := dialer.DialContext(sessionCtx, "tcp", upstreamAddr)
				if err != nil {
					_ = clientConn.Close()
					return
				}

				proxyConn(clientConn, upstreamConn)
			}()
		}
	}()

	expiresAt := time.Time{}
	if app.TTL > 0 {
		expiresAt = time.Now().Add(app.TTL)
	}

	return &Session{
		ID:        sessionID,
		App:       app,
		LocalAddr: ln.Addr().String(),
		StartedAt: time.Now(),
		ExpiresAt: expiresAt,
		PID:       os.Getpid(),
		Stop: func() {
			cancel()
			if closer != nil {
				_ = closer.Close()
			}
		},
	}, nil
}

func tcpDialAddress(app apps.AppConfig) string {
	if app.Type != apps.AppTypeTCP || !strings.EqualFold(strings.TrimSpace(app.Mode), "postgres") {
		return app.Upstream
	}
	u, err := url.Parse(strings.TrimSpace(app.Upstream))
	if err != nil {
		return app.Upstream
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return app.Upstream
	}
	if strings.TrimSpace(u.Host) == "" {
		return app.Upstream
	}
	return u.Host
}

func proxyConn(a, b net.Conn) {
	defer a.Close()
	defer b.Close()

	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(a, b)
		done <- struct{}{}
	}()

	go func() {
		_, _ = io.Copy(b, a)
		done <- struct{}{}
	}()

	<-done
}
