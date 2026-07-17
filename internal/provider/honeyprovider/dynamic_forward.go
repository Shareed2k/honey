package honeyprovider

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	socks5 "github.com/things-go/go-socks5"
)

// tunModeMaxChannels caps the number of concurrent CONNECT dials the SOCKS5
// proxy will issue against the upstream Honey proxy at once. Mirrors
// sshclient.tunModeMaxChannels: without a cap, background OS traffic
// (browser tabs, OS telemetry, etc.) could flood the upstream WS tunnel with
// hundreds of simultaneous dials.
const tunModeMaxChannels = 30

// passthroughResolver tells go-socks5 not to resolve DNS locally. Hostnames
// pass through to dialUpstream, which forwards them to the upstream Honey
// server for remote resolution — necessary since the local machine may not
// be able to resolve (or reach) names the upstream can. Mirrors
// sshclient.passthroughResolver.
type passthroughResolver struct{}

func (passthroughResolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, nil, nil
}

// StartDynamicForward starts a local SOCKS5 proxy on bind:localPort. Every
// CONNECT request is dialed via the upstream Honey proxy (c.dialUpstreamFn,
// defaulting to c.dialUpstream), so the server performs the real DNS
// resolution and dial — never the local machine.
//
// Every goroutine it spawns exits via the returned stop() (idempotent: it
// cancels the internal ctx, closes the listener, and waits for the serve
// loop to return).
func (c *Client) StartDynamicForward(ctx context.Context, bind string, localPort int) (host string, port int, stop func(), err error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(localPort)))
	if err != nil {
		return "", 0, nil, fmt.Errorf("honey dynamic forward listen: %w", err)
	}

	dial := c.dialer()
	ctx, cancel := context.WithCancel(ctx)

	sem := make(chan struct{}, tunModeMaxChannels)
	srv := socks5.NewServer(
		socks5.WithResolver(passthroughResolver{}),
		socks5.WithDial(func(_ context.Context, _ string, addr string) (net.Conn, error) {
			dialCtx, dialCancel := context.WithTimeout(ctx, 31*time.Second)
			defer dialCancel()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-time.After(5 * time.Second):
				return nil, fmt.Errorf("honey upstream channel limit reached")
			case <-dialCtx.Done():
				return nil, dialCtx.Err()
			}
			return dial(dialCtx, addr)
		}),
	)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ln)
	}()

	stop = sync.OnceFunc(func() {
		cancel()
		_ = ln.Close()
		wg.Wait()
	})

	ta := ln.Addr().(*net.TCPAddr)
	return ta.IP.String(), ta.Port, stop, nil
}
