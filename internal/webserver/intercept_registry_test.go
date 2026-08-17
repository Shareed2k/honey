package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/shareed2k/mogate/pkg/local"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/intercept"
)

// webOpts builds intercept.Options carrying only the fields the registry keys
// on (cluster/namespace/pod/actor/modes).
func webOpts(cluster, ns, pod string) intercept.Options {
	return intercept.Options{
		Cluster:   cluster,
		Namespace: ns,
		Pod:       pod,
		Actor:     "alice",
		Modes:     local.Modes{Egress: true},
	}
}

func noopCancel() {}

func TestWebInterceptRegistry_AdmitCapAndSamePod(t *testing.T) {
	r := newWebInterceptRegistry(nil, 2)
	ctx := context.Background()

	idA, err := r.admit(ctx, webOpts("prod", "apps", "a"), noopCancel)
	require.NoError(t, err)
	require.NotEmpty(t, idA)

	idB, err := r.admit(ctx, webOpts("prod", "apps", "b"), noopCancel)
	require.NoError(t, err)
	require.NotEqual(t, idA, idB)

	// Same (cluster, namespace, pod) as A: rejected with the collision message,
	// and the first session is untouched.
	_, err = r.admit(ctx, webOpts("prod", "apps", "a"), noopCancel)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already has an active interception")

	// A different pod, but the cap (2) is reached: rejected.
	_, err = r.admit(ctx, webOpts("prod", "apps", "c"), noopCancel)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max concurrent sessions (2)")

	// Distinct cluster with the same namespace/pod is a different pod: allowed.
	_ = idB
	r2 := newWebInterceptRegistry(nil, 8)
	_, err = r2.admit(ctx, webOpts("prod", "apps", "a"), noopCancel)
	require.NoError(t, err)
	_, err = r2.admit(ctx, webOpts("staging", "apps", "a"), noopCancel)
	require.NoError(t, err, "same namespace/pod in a different cluster is a distinct pod")
	_, err = r2.admit(ctx, webOpts("prod", "other", "a"), noopCancel)
	require.NoError(t, err, "same cluster/pod in a different namespace is a distinct pod")

	views, err := r.list(ctx)
	require.NoError(t, err)
	require.Len(t, views, 2, "only the two admitted sessions are listed")
}

// TestWebInterceptRegistry_NoTOCTOUDoubleAdmit_SamePod proves the admit path is
// atomic: many concurrent starts for the SAME pod result in exactly one admit.
func TestWebInterceptRegistry_NoTOCTOUDoubleAdmit_SamePod(t *testing.T) {
	// Cap high so the same-pod guard, not the cap, is the only limiter.
	r := newWebInterceptRegistry(nil, 1000)
	ctx := context.Background()

	const n = 50
	var (
		wg       sync.WaitGroup
		admitted int64
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := r.admit(ctx, webOpts("prod", "apps", "same"), noopCancel); err == nil {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, int64(1), atomic.LoadInt64(&admitted), "exactly one concurrent start for the same pod may be admitted")
	views, err := r.list(ctx)
	require.NoError(t, err)
	require.Len(t, views, 1)
}

// TestWebInterceptRegistry_NoTOCTOUDoubleAdmit_Cap proves the cap is enforced
// atomically under concurrent starts for DIFFERENT pods.
func TestWebInterceptRegistry_NoTOCTOUDoubleAdmit_Cap(t *testing.T) {
	r := newWebInterceptRegistry(nil, 1)
	ctx := context.Background()

	const n = 50
	var (
		wg       sync.WaitGroup
		admitted int64
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			pod := "pod-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
			if _, err := r.admit(ctx, webOpts("prod", "apps", pod), noopCancel); err == nil {
				atomic.AddInt64(&admitted, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	require.Equal(t, int64(1), atomic.LoadInt64(&admitted), "cap=1 admits exactly one concurrent start")
}

func TestWebInterceptRegistry_RemoveDeletesEntry(t *testing.T) {
	store := intercept.NewMemStore()
	r := newWebInterceptRegistry(store, 8)
	ctx := context.Background()

	id, err := r.admit(ctx, webOpts("prod", "apps", "a"), noopCancel)
	require.NoError(t, err)

	_, ok, err := store.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, ok)

	r.remove(ctx, id)

	_, ok, err = store.Get(ctx, id)
	require.NoError(t, err)
	require.False(t, ok, "remove deletes the store entry")

	views, err := r.list(ctx)
	require.NoError(t, err)
	require.Empty(t, views)

	// Removing again is harmless (idempotent).
	r.remove(ctx, id)
}

func TestWebInterceptRegistry_StopCancels(t *testing.T) {
	r := newWebInterceptRegistry(nil, 8)
	ctx := context.Background()

	var cancelled atomic.Bool
	id, err := r.admit(ctx, webOpts("prod", "apps", "a"), func() { cancelled.Store(true) })
	require.NoError(t, err)

	require.True(t, r.stop(id), "stop finds the live session")
	require.True(t, cancelled.Load(), "stop cancels the session context")

	require.False(t, r.stop("does-not-exist"), "stop of an unknown id reports not-found")
}

// TestWebInterceptRegistry_JanitorReapsExpired proves the janitor reaps an
// orphaned (lease-lapsed) browser entry, leaves a live one and a brokered one
// alone, and exits cleanly.
func TestWebInterceptRegistry_JanitorReapsExpired(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())

	store := intercept.NewMemStore()
	r := newWebInterceptRegistry(store, 8)
	base := time.Now()
	r.now = func() time.Time { return base }
	ctx := context.Background()

	// A live browser session (in the cancels map, not yet expired).
	liveID, err := r.admit(ctx, webOpts("prod", "apps", "live"), noopCancel)
	require.NoError(t, err)

	// An orphaned browser entry: in the store, past expiry, NOT in the cancels
	// map (its handler is gone) — a crashed handler in a persistent store.
	require.NoError(t, store.Save(ctx, intercept.PersistedSession{
		ID: "orphan", Cluster: "prod", Namespace: "apps", Pod: "orphan",
		StartedAt: base.Add(-2 * time.Hour), ExpiresAt: base.Add(-time.Hour),
	}))

	// A brokered entry (TokenHash set), also past expiry: NOT the browser
	// janitor's job — the Broker janitor owns it.
	require.NoError(t, store.Save(ctx, intercept.PersistedSession{
		ID: "brokered", Cluster: "prod", Namespace: "apps", Pod: "brokered",
		TokenHash: []byte{0x01}, ExpiresAt: base.Add(-time.Hour),
	}))

	n := r.reap(ctx)
	require.Equal(t, 1, n, "only the orphaned browser entry is reaped")

	_, ok, _ := store.Get(ctx, "orphan")
	require.False(t, ok, "orphan reaped")
	_, ok, _ = store.Get(ctx, liveID)
	require.True(t, ok, "live browser session is protected even past its (stale) lease")
	_, ok, _ = store.Get(ctx, "brokered")
	require.True(t, ok, "brokered entry left for the Broker janitor")

	// The janitor goroutine exits on ctx cancel.
	jctx, cancel := context.WithCancel(context.Background())
	done := r.startJanitor(jctx)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("janitor did not exit on ctx cancel")
	}
}

// TestServer_InterceptSessionsEndpoint lists active browser interceptions and
// requires auth.
func TestServer_InterceptSessionsEndpoint(t *testing.T) {
	factory := func(_ hosts.Record, _ intercept.Options, _ intercept.LocalRunner) (*intercept.Session, error) {
		return nil, nil
	}
	s := newTestServer(t, Options{
		Token:                   "secret",
		Config:                  interceptTestConfig(),
		InterceptSessionFactory: factory,
	})
	require.NotNil(t, s.webIntercepts)

	// The list route unions in tmux-backed resume sessions; keep this test
	// hermetic against a real tmux on the host by stubbing the runner empty.
	defer swapTmuxRun(func(...string) ([]byte, error) { return nil, errors.New("no tmux") })()

	// Seed two active sessions.
	_, err := s.webIntercepts.admit(context.Background(), webOpts("prod", "apps", "a"), noopCancel)
	require.NoError(t, err)
	_, err = s.webIntercepts.admit(context.Background(), webOpts("prod", "apps", "b"), noopCancel)
	require.NoError(t, err)

	// Without a token: 401.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/intercept/sessions", nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code, "the endpoint requires auth")

	// With the token: 200 and both sessions listed with their metadata.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/intercept/sessions", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var body struct {
		Sessions []webInterceptView `json:"sessions"`
	}
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	require.Len(t, body.Sessions, 2)
	pods := map[string]webInterceptView{}
	for _, v := range body.Sessions {
		pods[v.Pod] = v
		require.NotEmpty(t, v.ID)
		require.Equal(t, "alice", v.Actor)
		require.Equal(t, []string{"egress"}, v.Modes)
		require.False(t, v.StartedAt.IsZero())
	}
	require.Contains(t, pods, "a")
	require.Contains(t, pods, "b")
}

func TestServer_InterceptSessionStopEndpoint(t *testing.T) {
	factory := func(_ hosts.Record, _ intercept.Options, _ intercept.LocalRunner) (*intercept.Session, error) {
		return nil, nil
	}
	s := newTestServer(t, Options{
		Token:                   "secret",
		Config:                  interceptTestConfig(),
		InterceptSessionFactory: factory,
	})

	var cancelled atomic.Bool
	id, err := s.webIntercepts.admit(context.Background(), webOpts("prod", "apps", "a"), func() { cancelled.Store(true) })
	require.NoError(t, err)

	// Unknown id: 404.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/intercept/sessions/nope/stop", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)

	// Known id: 204 and the session context is cancelled.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/intercept/sessions/"+id+"/stop", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.True(t, cancelled.Load())
}

// TestInterceptConfig_MaxSessionsValue covers the cap default.
func TestInterceptConfig_MaxSessionsValue(t *testing.T) {
	require.Equal(t, 8, (*config.InterceptConfig)(nil).MaxSessionsValue())
	require.Equal(t, 8, (&config.InterceptConfig{}).MaxSessionsValue())
	require.Equal(t, 8, (&config.InterceptConfig{MaxSessions: -3}).MaxSessionsValue())
	require.Equal(t, 3, (&config.InterceptConfig{MaxSessions: 3}).MaxSessionsValue())
}
