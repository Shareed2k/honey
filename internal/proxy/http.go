package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/shareed2k/honey/internal/apps"
)

// StartHTTPProxy constructs an HTTP reverse proxy and optionally binds it to 127.0.0.1.
// If app.LocalPort is 0, it skips binding the listener (used by the webserver dynamic proxy).
func StartHTTPProxy(ctx context.Context, app apps.AppConfig, dialer Dialer, sessionID string) (*Session, error) {
	targetURL := &url.URL{
		Scheme: "http",
		Host:   app.Upstream,
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	transport := &http.Transport{
		DialContext: dialer.DialContext,
	}
	proxy.Transport = transport

	sessionCtx, cancel := context.WithCancel(ctx)

	expiresAt := time.Time{}
	if app.TTL > 0 {
		expiresAt = time.Now().Add(app.TTL)
	}

	sess := &Session{
		ID:        sessionID,
		App:       app,
		StartedAt: time.Now(),
		ExpiresAt: expiresAt,
		PID:       os.Getpid(),
		Handler:   proxy,
		Stop:      cancel,
	}

	// If LocalPort is specified, bind a local listener (used by CLI `honey app open`)
	if app.LocalPort > 0 {
		srv := &http.Server{
			Addr:              fmt.Sprintf("127.0.0.1:%d", app.LocalPort),
			Handler:           proxy,
			ReadHeaderTimeout: 5 * time.Second, // mitigate G112
		}

		// #nosec G118 -- intentional background context for shutdown because sessionCtx is already done
		go func() {
			<-sessionCtx.Done()
			// Use a bounded context instead of context.Background() for the shutdown
			shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShutdown()
			_ = srv.Shutdown(shutdownCtx)
		}()

		errCh := make(chan error, 1)
		go func() {
			errCh <- srv.ListenAndServe()
		}()

		// Wait briefly to ensure we bound to the port
		select {
		case err := <-errCh:
			cancel()
			if err != nil && err != http.ErrServerClosed {
				return nil, fmt.Errorf("failed to start http proxy: %w", err)
			}
		case <-time.After(100 * time.Millisecond):
			// Assume started
		}
		sess.LocalAddr = srv.Addr
	} else {
		// When LocalPort is 0 (dynamic web proxy), we do not bind a listener,
		// but we still want to auto-cancel if the context closes.
		go func() {
			<-sessionCtx.Done()
		}()
	}

	return sess, nil
}
