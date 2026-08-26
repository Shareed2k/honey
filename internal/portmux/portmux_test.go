package portmux

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// cmux's matcher goroutines and http.Server's conn goroutines can outlive
		// a closed listener by a few scheduler ticks; IgnoreCurrent keeps this
		// package's own leak check from tripping on the runtime's background
		// goroutines while still catching a listener that never unwinds.
		goleak.IgnoreCurrent(),
	)
}

// startMux binds a loopback port, serves HTTP on one half and an
// identification-string echo on the other, and returns the address.
func startMux(t *testing.T) (addr string, sshBanners <-chan string) {
	t.Helper()

	base, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	m := New(base)

	banners := make(chan string, 4)
	done := make(chan struct{})

	// SSH half: stand in for the real gateway by reading the client's
	// identification line — enough to prove the connection was routed here and
	// arrived intact (nothing consumed the sniffed prefix).
	go func() {
		for {
			c, aerr := m.SSH.Accept()
			if aerr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				line, rerr := bufio.NewReader(c).ReadString('\n')
				if rerr != nil {
					return
				}
				select {
				case banners <- strings.TrimRight(line, "\r\n"):
				default:
				}
			}(c)
		}
	}()

	srv := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "web-half")
		}),
	}
	go func() { _ = srv.Serve(m.HTTP) }()
	go func() { defer close(done); _ = m.Serve() }()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = m.Close()
		<-done
	})
	return base.Addr().String(), banners
}

func TestMux_RoutesSSHIdentificationToSSHHalf(t *testing.T) {
	addr, banners := startMux(t)

	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	const ident = "SSH-2.0-OpenSSH_9.6"
	_, err = io.WriteString(c, ident+"\r\n")
	require.NoError(t, err)

	select {
	case got := <-banners:
		// The whole line must survive: cmux hands the peeked bytes back to the
		// matched listener, so the SSH half sees the stream from byte zero.
		require.Equal(t, ident, got)
	case <-time.After(5 * time.Second):
		t.Fatal("SSH connection was not routed to the SSH half")
	}
}

func TestMux_RoutesHTTPToWebHalf(t *testing.T) {
	addr, _ := startMux(t)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + addr + "/")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "web-half", string(body))
}

// A client whose first bytes are neither an SSH identification string nor a
// recognizable HTTP request must still land somewhere rather than hanging the
// listener: cmux.Any() is the fallback, so the HTTP server answers (with 400).
func TestMux_UnknownProtocolFallsBackToWebHalf(t *testing.T) {
	addr, _ := startMux(t)

	c, err := net.Dial("tcp", addr)
	require.NoError(t, err)
	defer func() { _ = c.Close() }()

	_, err = io.WriteString(c, "GARBAGE\r\n\r\n")
	require.NoError(t, err)

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, err := c.Read(buf)
	require.NoError(t, err, "unknown-protocol connection got no response at all")
	require.Contains(t, string(buf[:n]), "HTTP/1.1 400")
}

// Close must unblock Serve — otherwise `honey web` shutdown would hang on the
// mux instead of exiting.
func TestMux_CloseUnblocksServe(t *testing.T) {
	base, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	m := New(base)

	served := make(chan error, 1)
	go func() { served <- m.Serve() }()

	require.NoError(t, m.Close())
	select {
	case <-served:
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after Close")
	}
}
