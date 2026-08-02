package honeyprovider

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/tun"
)

// TestStartTunForward_RequiresRoot covers the root guard on
// Client.StartTunForward: it must fail fast (mirroring cli/egress.go's
// `--tun` guard) without ever starting the dynamic forward or the tun
// runner, since both require privileges tun2proxy needs (raw TUN device
// creation) that a non-root process cannot grant.
func TestStartTunForward_RequiresRoot(t *testing.T) {
	dfCalled := false
	tunCalled := false
	c := &Client{
		getuidFn: func() int { return 501 },
		startDynamicForwardFn: func(context.Context, string, int) (string, int, func(), error) {
			dfCalled = true
			return "", 0, nil, nil
		},
		tunRunFn: func(context.Context, tun.Config) error {
			tunCalled = true
			return nil
		},
	}

	tunName, stop, err := c.StartTunForward(context.Background(), "ubuntu", "myhost", 22, 0, 0)
	require.Error(t, err)
	require.Contains(t, err.Error(), "root")
	require.Empty(t, tunName)
	require.Nil(t, stop)
	require.False(t, dfCalled, "dynamic forward must not start when the root guard fails")
	require.False(t, tunCalled, "tun runner must not start when the root guard fails")
}

// TestStartTunForward_Composition drives Client.StartTunForward entirely
// through injected fakes (getuidFn, startDynamicForwardFn, tunRunFn) -- no
// root privileges or real tun2proxy binary required -- and asserts the
// composition: the tun runner receives the SOCKSHost/SOCKSPort produced by
// the dynamic forward, and stop() tears both down (cancels the tun runner's
// ctx and calls the dynamic forward's stop), leaving no goroutines behind.
func TestStartTunForward_Composition(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/shareed2k/honey/internal/engine.(*GlobalTunnelPool).sweepLoop"))

	var mu sync.Mutex
	dfStopCalls := 0

	c := &Client{
		record:   hosts.Record{Name: "myhost", PrimaryIP: "10.0.0.5"},
		getuidFn: func() int { return 0 },
		startDynamicForwardFn: func(_ context.Context, bind string, localPort int) (string, int, func(), error) {
			require.Equal(t, "127.0.0.1", bind)
			require.Equal(t, 0, localPort)
			stop := func() {
				mu.Lock()
				dfStopCalls++
				mu.Unlock()
			}
			return "127.0.0.1", 1080, stop, nil
		},
	}

	gotCfgCh := make(chan tun.Config, 1)
	c.tunRunFn = func(ctx context.Context, cfg tun.Config) error {
		gotCfgCh <- cfg
		<-ctx.Done() // block until StartTunForward's stop() cancels this ctx
		return ctx.Err()
	}

	tunName, stop, err := c.StartTunForward(context.Background(), "ubuntu", "myhost", 22, 4, 5)
	require.NoError(t, err)
	require.NotEmpty(t, tunName)
	require.NotNil(t, stop)

	select {
	case cfg := <-gotCfgCh:
		require.Equal(t, "127.0.0.1", cfg.SOCKSHost)
		require.Equal(t, 1080, cfg.SOCKSPort)
		require.Equal(t, "myhost", cfg.HostName)
		require.Equal(t, []string{"10.0.0.5"}, cfg.SSHIPs)
	case <-time.After(2 * time.Second):
		t.Fatal("tunRunFn was not invoked with the dynamic forward's host/port")
	}

	stop()

	mu.Lock()
	calls := dfStopCalls
	mu.Unlock()
	require.Equal(t, 1, calls, "stop() must call the dynamic forward's stop exactly once")

	stop() // idempotent: must not panic or double-stop the dynamic forward
	mu.Lock()
	calls = dfStopCalls
	mu.Unlock()
	require.Equal(t, 1, calls, "stop() must be idempotent")
}
