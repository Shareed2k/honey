package honeyprovider

import (
	"context"
	"io"
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	// GlobalTunnelPool starts a process-lifetime background sweep goroutine
	// from a package-level var in internal/engine (imported transitively by
	// this test package via exec_test.go). It has no test-scoped shutdown
	// hook and is unrelated to the code under test here, so it is excluded
	// from the leak check by name rather than ignored wholesale.
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))
}

func TestListenAndPipe_LocalForward(t *testing.T) {
	fake := func(_ context.Context, _ string) (net.Conn, error) {
		cli, srv := net.Pipe()
		go func() { io.Copy(srv, srv); srv.Close() }() // echo
		return cli, nil
	}
	host, port, stop, err := listenAndPipe(context.Background(), "127.0.0.1", 0, fake,
		func(net.Conn) (string, error) { return "ignored:0", nil })
	require.NoError(t, err)
	defer stop()
	c, err := net.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	require.NoError(t, err)
	defer c.Close()
	_, _ = c.Write([]byte("ping"))
	buf := make([]byte, 4)
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
	require.Equal(t, "ping", string(buf))
}
