package ui

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
	"github.com/shareed2k/honey/internal/stepkv"
)

type recipeKVForward struct {
	stop func()
	env  map[string]string
}

// RecipeKVCoordinator owns one operator-side stepkv session for a cue-exec run and attaches one SSH
// remote-forward per cached HoneyClient host key when cue-exec runs a step with kv_tunnel over SSH.
// For Kubernetes pod targets with recipe-scoped kv_tunnel, it starts a long-lived exec bridge per pod so
// in-pod HTTP clients reach the same stepkv session (shared namespace with SSH in the same run).
type RecipeKVCoordinator struct {
	mu       sync.Mutex
	ttl      time.Duration
	sess     *stepkv.Session
	forwards map[string]*recipeKVForward
	closed   bool
}

// NewRecipeKVCoordinator constructs a coordinator; ttl defaults to stepKVTunnelTTL when <= 0.
func NewRecipeKVCoordinator(ttl time.Duration) *RecipeKVCoordinator {
	if ttl <= 0 {
		ttl = stepKVTunnelTTL
	}
	return &RecipeKVCoordinator{
		ttl:      ttl,
		forwards: make(map[string]*recipeKVForward),
	}
}

// EnsureKVTunnelEnv returns HONEY_KV_* for this host's remote-forward into the shared session, creating
// the session and/or forward on first use.
func (c *RecipeKVCoordinator) EnsureKVTunnelEnv(user string, r hosts.Record, hc *sshclient.HoneyClient) (map[string]string, error) {
	if c == nil {
		return nil, fmt.Errorf("recipe kv: nil coordinator")
	}
	key := SSHClientCacheKey(user, r)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("recipe kv: coordinator closed")
	}
	if fwd, ok := c.forwards[key]; ok {
		return maps.Clone(fwd.env), nil
	}
	if c.sess == nil {
		sess, err := stepkv.Start(c.ttl)
		if err != nil {
			return nil, err
		}
		c.sess = sess
	}
	env, stopForward, err := attachKVRemoteForwardToSession(hc, c.sess)
	if err != nil {
		return nil, err
	}
	c.forwards[key] = &recipeKVForward{stop: stopForward, env: env}
	return maps.Clone(env), nil
}

// EnsureK8sExecBridgeEnv returns HONEY_KV_* for this pod by multiplexing pod loopback HTTP to the shared
// stepkv session over a long-lived kubectl exec (same key namespace as SSH for this run).
func (c *RecipeKVCoordinator) EnsureK8sExecBridgeEnv(user string, r hosts.Record, k8c *k8sNativeClient) (map[string]string, error) {
	if c == nil {
		return nil, fmt.Errorf("recipe kv: nil coordinator")
	}
	key := SSHClientCacheKey(user, r)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, fmt.Errorf("recipe kv: coordinator closed")
	}
	if fwd, ok := c.forwards[key]; ok {
		return maps.Clone(fwd.env), nil
	}
	if c.sess == nil {
		sess, err := stepkv.Start(c.ttl)
		if err != nil {
			return nil, err
		}
		c.sess = sess
	}
	env, stopBridge, err := startK8sRecipeKVExecBridge(context.Background(), k8c, c.sess)
	if err != nil {
		return nil, err
	}
	c.forwards[key] = &recipeKVForward{stop: stopBridge, env: env}
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
	if !ok || fwd == nil || fwd.stop == nil {
		return
	}
	fwd.stop()
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
		if fwd != nil && fwd.stop != nil {
			fwd.stop()
		}
	}
	if sess != nil {
		_ = sess.Close()
	}
}
