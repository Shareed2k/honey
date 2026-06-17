package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/stepkv"
)

type recipeKVForward struct {
	stop  func()
	env   map[string]string
	ready chan struct{} // closed when stop/env are populated
	err   error
}

// RecipeKVCoordinator owns one operator-side stepkv session for a cue-exec run and one forward (SSH
// remote-forward or k8s exec bridge) per cached client key. The mutex is only held while the per-key
// placeholder is reserved; the slow handshake runs outside the lock so parallel hosts don't serialize.
// RecipeKVCoordinator ...
type RecipeKVCoordinator struct {
	mu       sync.Mutex
	ttl      time.Duration
	sess     *stepkv.Session
	forwards map[string]*recipeKVForward
	closed   bool
}

// NewRecipeKVCoordinator constructs a coordinator; ttl defaults to stepKVTunnelTTL when <= 0.
// NewRecipeKVCoordinator ...
func NewRecipeKVCoordinator(ttl time.Duration) *RecipeKVCoordinator {
	if ttl <= 0 {
		ttl = stepKVTunnelTTL
	}
	return &RecipeKVCoordinator{
		ttl:      ttl,
		forwards: make(map[string]*recipeKVForward),
	}
}

// EnsureSession returns the shared stepkv session, creating it if needed (no SSH forward).
func (c *RecipeKVCoordinator) EnsureSession() (*stepkv.Session, error) {
	if c == nil {
		return nil, errors.New("recipe kv: nil coordinator")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("recipe kv: coordinator closed")
	}
	return c.ensureSessionLocked()
}

// EnsureKVTunnelEnv returns HONEY_KV_* for this host's remote-forward into the shared session.
func (c *RecipeKVCoordinator) EnsureKVTunnelEnv(user string, r hosts.Record, hc *sshclient.HoneyClient) (map[string]string, error) {
	if c == nil {
		return nil, errors.New("recipe kv: nil coordinator")
	}
	return c.ensureForward(user, r, func(sess *stepkv.Session) (map[string]string, func(), error) {
		return attachKVRemoteForwardToSession(hc, sess)
	})
}

// EnsureK8sExecBridgeEnv returns HONEY_KV_* for this pod by multiplexing pod loopback HTTP to the shared
// stepkv session over a long-lived kubectl exec.
func (c *RecipeKVCoordinator) EnsureK8sExecBridgeEnv(user string, r hosts.Record, k8c *K8sNativeClient) (map[string]string, error) {
	if c == nil {
		return nil, errors.New("recipe kv: nil coordinator")
	}
	return c.ensureForward(user, r, func(sess *stepkv.Session) (map[string]string, func(), error) {
		return startK8sRecipeKVExecBridge(context.Background(), k8c, sess)
	})
}

func (c *RecipeKVCoordinator) ensureForward(user string, r hosts.Record, attach func(*stepkv.Session) (map[string]string, func(), error)) (map[string]string, error) {
	key := SSHClientCacheKey(user, r)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("recipe kv: coordinator closed")
	}
	if fwd, ok := c.forwards[key]; ok {
		c.mu.Unlock()
		<-fwd.ready
		if fwd.err != nil {
			return nil, fwd.err
		}
		return maps.Clone(fwd.env), nil
	}
	if _, err := c.ensureSessionLocked(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	fwd := &recipeKVForward{ready: make(chan struct{})}
	c.forwards[key] = fwd
	sess := c.sess
	c.mu.Unlock()

	env, stop, err := attach(sess)
	c.mu.Lock()
	if err != nil {
		delete(c.forwards, key)
		fwd.err = err
		close(fwd.ready)
		c.mu.Unlock()
		return nil, fmt.Errorf("recipe kv: %w", err)
	}
	if c.closed {
		c.mu.Unlock()
		if stop != nil {
			stop()
		}
		fwd.err = errors.New("recipe kv: coordinator closed")
		close(fwd.ready)
		return nil, fwd.err
	}
	fwd.env = env
	fwd.stop = stop
	close(fwd.ready)
	c.mu.Unlock()
	return maps.Clone(env), nil
}

// InvalidateHost tears down this host's remote-forward or k8s exec bridge only (e.g. after cache evict).
// The shared stepkv session stays open for other hosts.
func (c *RecipeKVCoordinator) InvalidateHost(user string, r hosts.Record) {
	if c == nil {
		return
	}
	key := SSHClientCacheKey(user, r)
	c.mu.Lock()
	fwd, ok := c.forwards[key]
	if ok {
		delete(c.forwards, key)
	}
	c.mu.Unlock()
	if !ok || fwd == nil {
		return
	}
	<-fwd.ready
	if fwd.stop != nil {
		fwd.stop()
	}
}

// Close stops all remote listeners / k8s bridges and closes the stepkv session.
func (c *RecipeKVCoordinator) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	old := c.forwards
	c.forwards = make(map[string]*recipeKVForward)
	sess := c.sess
	c.sess = nil
	c.closed = true
	c.mu.Unlock()
	for _, fwd := range old {
		if fwd == nil {
			continue
		}
		<-fwd.ready
		if fwd.stop != nil {
			fwd.stop()
		}
	}
	if sess != nil {
		_ = sess.Close()
	}
}

func (c *RecipeKVCoordinator) ensureSessionLocked() (*stepkv.Session, error) {
	if c.sess != nil {
		return c.sess, nil
	}
	sess, err := stepkv.Start(c.ttl)
	if err != nil {
		return nil, err
	}
	c.sess = sess
	return c.sess, nil
}
