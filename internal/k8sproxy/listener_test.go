package k8sproxy

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServe_BindError(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := generateServingCert(nil)
	require.NoError(t, err)
	tlsCfg, err := BuildServerTLSConfig(certPEM, keyPEM, ca.certPEM)
	require.NoError(t, err)

	// A malformed address fails the net.Listen and surfaces as an error.
	err = Serve(context.Background(), "256.256.256.256:0", tlsCfg, http.NewServeMux())
	require.Error(t, err)
}

func TestServe_ShutdownOnContextCancel(t *testing.T) {
	ca := newTestCA(t)
	certPEM, keyPEM, err := generateServingCert(nil)
	require.NoError(t, err)
	tlsCfg, err := BuildServerTLSConfig(certPEM, keyPEM, ca.certPEM)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, "127.0.0.1:0", tlsCfg, http.NewServeMux())
	}()

	// Give Serve a moment to bind + start serving, then cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.NoError(t, err, "ctx cancel is a clean shutdown")
	case <-time.After(shutdownGrace + 2*time.Second):
		t.Fatal("Serve did not return after context cancel within the grace window")
	}
}
