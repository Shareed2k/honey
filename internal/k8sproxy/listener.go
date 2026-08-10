package k8sproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// shutdownGrace bounds how long Serve waits for in-flight connections to unwind
// after ctx is cancelled. kubectl exec / logs -f / port-forward hijack the
// connection into a long-lived stream that http.Server.Shutdown does not track;
// once this elapses Serve force-closes so Ctrl+C never hangs on a stuck stream.
const shutdownGrace = 5 * time.Second

// Serve binds a TLS listener on addr and serves h until ctx is cancelled. On
// cancellation it performs a bounded-drain shutdown (graceful Shutdown capped at
// shutdownGrace, then a hard Close) so hijacked long-lived connections cannot
// wedge shutdown. It returns the serve error, treating http.ErrServerClosed as a
// clean stop.
func Serve(ctx context.Context, addr string, tlsCfg *tls.Config, h http.Handler) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("k8sproxy: listen %q: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           h,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		// The serving cert/key are already in tlsCfg, so empty file args here.
		errCh <- srv.ServeTLS(ln, "", "")
	}()

	select {
	case err := <-errCh:
		return ignoreServerClosed(err)
	case <-ctx.Done():
		shCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if serr := srv.Shutdown(shCtx); serr != nil {
			// Grace elapsed with connections still draining: hard-close so we
			// never block indefinitely on a hijacked stream.
			_ = srv.Close()
		}
		// Shutdown/Close both stop ServeTLS with http.ErrServerClosed; drain it.
		return ignoreServerClosed(<-errCh)
	}
}

func ignoreServerClosed(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
