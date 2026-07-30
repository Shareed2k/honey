package sshclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/iamcalledrob/netstatus"
	"github.com/jackc/puddle/v2"
	"go.uber.org/zap"
	gossh "golang.org/x/crypto/ssh"
)

const (
	poolKeepaliveInterval  = 10 * time.Second
	poolKeepaliveTimeout   = 5 * time.Second
	poolChannelDialTimeout = 10 * time.Second
	rebuildDebounce        = 15 * time.Second
)

// SSHDialer is the minimal interface for routing SOCKS5 connections over SSH.
// *gossh.Client satisfies this interface without any wrapping.
type SSHDialer interface {
	Dial(network, addr string) (net.Conn, error)
}

// SSHPool maintains up to size parallel HoneyClient connections to one host.
// puddle manages the resource lifecycle; a background goroutine probes idle
// connections and destroys dead ones before any dial hits them.
type SSHPool struct {
	pool        *puddle.Pool[*HoneyClient]
	cancel      context.CancelFunc
	lastRebuild time.Time
	rebuildMu   sync.Mutex
}

// NewSSHPool creates and eagerly warms a pool of size SSH connections using dialFn.
// All size connections are established before the call returns.
func NewSSHPool(ctx context.Context, size int, dialFn func() (*HoneyClient, error)) (*SSHPool, error) {
	if size < 1 {
		size = 1
	}
	// kCtx must be created before the pool so the constructor closure can capture it.
	// When kCtx is cancelled (Close), any in-progress construction unblocks immediately
	// regardless of which context puddle passed to the constructor.
	kCtx, cancel := context.WithCancel(ctx)
	p, err := puddle.NewPool(&puddle.Config[*HoneyClient]{
		Constructor: func(puddleCtx context.Context) (*HoneyClient, error) {
			type result struct {
				hc  *HoneyClient
				err error
			}
			ch := make(chan result, 1)
			go func() {
				hc, err := dialFn()
				ch <- result{hc, err}
			}()
			drain := func(err error) (*HoneyClient, error) {
				go func() {
					if r := <-ch; r.hc != nil {
						_ = r.hc.Close()
					}
				}()
				return nil, err
			}
			select {
			case r := <-ch:
				return r.hc, r.err
			case <-puddleCtx.Done():
				return drain(puddleCtx.Err())
			case <-kCtx.Done():
				return drain(kCtx.Err())
			}
		},
		Destructor: func(hc *HoneyClient) { _ = hc.Close() },
		MaxSize:    int32(size),
	})
	if err != nil {
		cancel()
		return nil, err
	}
	for i := 0; i < size; i++ {
		if err := p.CreateResource(kCtx); err != nil {
			p.Close()
			cancel()
			return nil, fmt.Errorf("pool entry %d: %w", i, err)
		}
		zap.L().Debug("ssh pool entry created", zap.Int("index", i), zap.Int("size", size))
	}
	sp := &SSHPool{pool: p, cancel: cancel}
	go sp.keepaliveLoop(kCtx)
	go sp.netWatcher(kCtx)

	zap.L().Debug("ssh pool ready", zap.Int("size", size))

	return sp, nil
}

// DialContext acquires a pool entry, opens an SSH channel, then immediately
// releases the entry back to the pool. ctx cancellation stops the retry loop
// immediately so no goroutine outlives the caller.
func (sp *SSHPool) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 500 * time.Millisecond
	conn, err := backoff.Retry(ctx, func() (net.Conn, error) {
		res, err := sp.pool.Acquire(ctx)
		if err != nil {
			if errors.Is(err, puddle.ErrClosedPool) ||
				errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded) {
				return nil, backoff.Permanent(err)
			}
			zap.L().Debug("ssh pool acquire failed, will retry", zap.Error(err))
			return nil, err
		}
		c, err := sshDialConn(res.Value().LeafSSH(), network, addr)
		if err != nil {
			if _, ok := errors.AsType[*gossh.OpenChannelError](err); ok {
				res.Destroy()
				zap.L().Debug("ssh channel open failed, destroying entry and retrying", zap.Error(err))
				return nil, err
			}
			res.Destroy()
			zap.L().Debug("ssh dial failed, will retry", zap.Error(err))
			return nil, err
		}
		res.Release()
		return c, nil
	}, backoff.WithBackOff(bo), backoff.WithMaxElapsedTime(30*time.Second))
	if err != nil {
		return nil, fmt.Errorf("ssh pool dial: %w", err)
	}
	return conn, nil
}

// Dial implements SSHDialer using a background context.
// The SOCKS5 path calls DialContext directly to avoid goroutine leaks.
func (sp *SSHPool) Dial(network, addr string) (net.Conn, error) {
	return sp.DialContext(context.Background(), network, addr)
}

// RunWithClient acquires a pool entry and calls fn with its underlying ssh.Client.
// Useful for one-shot SSH operations (e.g. remote route discovery) that need a
// session without going through the SOCKS5 path.
func (sp *SSHPool) RunWithClient(ctx context.Context, fn func(*gossh.Client) error) error {
	res, err := sp.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("ssh pool acquire: %w", err)
	}
	if err := fn(res.Value().LeafSSH()); err != nil {
		res.Destroy()
		return err
	}
	res.Release()
	return nil
}

// Close stops the keepalive loop and destroys all connections in the pool.
func (sp *SSHPool) Close() error {
	zap.L().Debug("ssh pool closed")
	sp.cancel()
	sp.pool.Close()
	return nil
}

func (sp *SSHPool) netWatcher(ctx context.Context) {
	m := netstatus.StartMonitor(ctx)
	wasDown := !m.Current(ctx).Available
	m.OnChange(func(s netstatus.Status) {
		if wasDown && s.Available {
			zap.L().Info("network restored, rebuilding ssh pool")
			sp.rebuildAll(ctx)
		}
		wasDown = !s.Available
	})
	<-ctx.Done()
}

// sshDialConn opens a direct-tcp channel with a timeout. Prevents hanging
// indefinitely on half-dead SSH connections (e.g. right after WiFi reconnect).
func sshDialConn(ssh *gossh.Client, network, addr string) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		conn, err := ssh.Dial(network, addr)
		ch <- result{conn, err}
	}()
	select {
	case r := <-ch:
		return r.conn, r.err
	case <-time.After(poolChannelDialTimeout):
		return nil, fmt.Errorf("ssh channel dial timeout")
	}
}

// sendKeepalive sends a keepalive request with a timeout. Returns an error if
// the request fails or the timeout elapses (treating timeout as a dead connection).
func sendKeepalive(ssh *gossh.Client) error {
	ch := make(chan error, 1)
	go func() {
		_, _, err := ssh.SendRequest("keepalive@openssh.com", true, nil)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(poolKeepaliveTimeout):
		return fmt.Errorf("keepalive timeout")
	}
}

// Healthy reports whether the HoneyClient's underlying SSH connection still
// answers a keepalive within poolKeepaliveTimeout. A nil leaf (no active SSH
// client to probe) is reported healthy. Both the pool's keepalive loop and the
// engine ClientCache liveness check resolve to the same timeout-guarded
// sendKeepalive, so a half-dead socket cannot hang either caller — previously
// the ClientCache probe issued a raw SendRequest with no timeout guard.
func (h *HoneyClient) Healthy() error {
	leaf := h.LeafSSH()
	if leaf == nil {
		return nil
	}
	return sendKeepalive(leaf)
}

func (sp *SSHPool) rebuildAll(ctx context.Context) {
	sp.rebuildMu.Lock()
	if time.Since(sp.lastRebuild) < rebuildDebounce {
		sp.rebuildMu.Unlock()
		zap.L().Debug("ssh pool rebuild skipped (debounce)")
		return
	}
	sp.lastRebuild = time.Now()
	sp.rebuildMu.Unlock()

	// Always destroy idle entries — old connections may pass keepalive but fail
	// channel opens after WiFi reconnect, leaving zombie entries in the pool.
	for _, res := range sp.pool.AcquireAllIdle() {
		res.Destroy()
	}

	// Fill any deficit (entries destroyed above + entries already missing from keepalive).
	stat := sp.pool.Stat()
	deficit := int(stat.MaxResources()) - int(stat.TotalResources())
	for range deficit {
		go sp.createWithBackoff(ctx)
	}
}

func (sp *SSHPool) createWithBackoff(ctx context.Context) {
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = 500 * time.Millisecond
	_, retryErr := backoff.Retry(ctx, func() (struct{}, error) {
		if createErr := sp.pool.CreateResource(ctx); createErr != nil {
			if errors.Is(createErr, puddle.ErrClosedPool) {
				return struct{}{}, backoff.Permanent(createErr)
			}
			zap.L().Debug("ssh pool rebuild attempt failed, will retry", zap.Error(createErr))
			return struct{}{}, createErr
		}
		zap.L().Debug("ssh pool rebuild ok")
		return struct{}{}, nil
	}, backoff.WithBackOff(bo), backoff.WithMaxElapsedTime(30*time.Second))
	if retryErr != nil && !errors.Is(retryErr, puddle.ErrClosedPool) {
		zap.L().Warn("ssh pool rebuild gave up", zap.Error(retryErr))
	}
}

func (sp *SSHPool) keepaliveLoop(ctx context.Context) {
	ticker := time.NewTicker(poolKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, res := range sp.pool.AcquireAllIdle() {
				if err := sendKeepalive(res.Value().LeafSSH()); err != nil {
					zap.L().Warn("ssh pool keepalive failed, destroying entry")
					res.Destroy()
				} else {
					zap.L().Debug("ssh pool keepalive ok")
					res.Release()
				}
			}
			// Fill any deficit — covers entries destroyed by Dial retries as well
			// as entries destroyed above by keepalive failure.
			stat := sp.pool.Stat()
			deficit := int(stat.MaxResources()) - int(stat.TotalResources())
			for range deficit {
				go sp.createWithBackoff(ctx)
			}
		}
	}
}
