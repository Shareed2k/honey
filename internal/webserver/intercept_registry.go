package webserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/intercept"
)

// webInterceptTTL bounds a browser interception's REGISTRY entry (not the
// session itself). While the WebSocket is live the handler re-saves the entry
// every webInterceptHeartbeat, so a healthy session's entry never lapses and is
// never reaped; once the handler exits — cleanly, or by crashing without running
// its deregister — the entry lapses within this window and the TTL janitor reaps
// the orphan. It is deliberately short because the heartbeat keeps live entries
// fresh: the only entries that reach expiry are abandoned ones (a crashed
// handler in a persistent store), which should be cleaned up promptly so the cap
// and the same-pod guard do not count a session that no longer exists.
const (
	webInterceptTTL       = 90 * time.Second
	webInterceptHeartbeat = 30 * time.Second
)

// webInterceptRegistry tracks the live browser (/ws/intercept) interception
// sessions: it enforces the concurrency cap and the same-pod collision guard on
// admission, keeps each entry's lease fresh, lists the active sessions for the
// UI, and can stop one by id. Active state lives in an intercept.SessionStore
// (reused from the Broker when one exists, so brokered and browser sessions
// share a single registry and the same-pod guard sees both); the id -> cancel
// map holds the in-process cancel funcs the store cannot, so a session can be
// torn down by id. Safe for concurrent use.
type webInterceptRegistry struct {
	// mu guards the whole admit critical section (list -> check -> save) so two
	// concurrent starts can never both pass the cap or the same-pod check
	// (no TOCTOU double-admit), and guards the cancels map.
	mu      sync.Mutex
	store   intercept.SessionStore
	cancels map[string]context.CancelFunc

	max               int
	ttl               time.Duration
	heartbeatInterval time.Duration
	now               func() time.Time
}

// newWebInterceptRegistry builds a registry over store, defaulting to a fresh
// in-memory store when store is nil (no Broker present). maxSessions <= 0
// selects the built-in default cap.
func newWebInterceptRegistry(store intercept.SessionStore, maxSessions int) *webInterceptRegistry {
	if store == nil {
		store = intercept.NewMemStore()
	}
	if maxSessions <= 0 {
		maxSessions = config.DefaultMaxInterceptSessions
	}
	return &webInterceptRegistry{
		store:             store,
		cancels:           make(map[string]context.CancelFunc),
		max:               maxSessions,
		ttl:               webInterceptTTL,
		heartbeatInterval: webInterceptHeartbeat,
		now:               time.Now,
	}
}

// isWebIntercept reports whether ps is a browser (/ws/intercept) session rather
// than a server-brokered one. Broker.Authorize always mints a per-session agent
// token and stores its sha256, so a brokered entry's TokenHash is never empty;
// the browser terminal runs a direct Session with no brokered token, so an empty
// TokenHash uniquely identifies a browser-interception entry in a shared store.
func isWebIntercept(ps intercept.PersistedSession) bool {
	return len(ps.TokenHash) == 0
}

// samePodActive reports whether the shared store already holds an interception
// for opts' (cluster, namespace, pod) — brokered OR browser. It is the
// load-bearing collision guard: an interception programs a fixed nftables table
// and the agent binds fixed ports (30000, 30001, 15080), so a second
// interception in the same pod would fight the first.
//
// It deliberately does NOT take r.mu: admit calls it while holding the lock (so
// its list -> check -> save stays atomic), and the tmux resume path — which
// registers no entry of its own and so cannot see brokered sessions any other
// way — calls it unlocked before starting a pane.
func (r *webInterceptRegistry) samePodActive(ctx context.Context, opts intercept.Options) (bool, error) {
	sessions, err := r.store.List(ctx)
	if err != nil {
		return false, fmt.Errorf("intercept: list active sessions: %w", err)
	}
	for _, s := range sessions {
		if s.Cluster == opts.Cluster && s.Namespace == opts.Namespace && s.Pod == opts.Pod {
			return true, nil
		}
	}
	return false, nil
}

// errSamePodActive is the single rejection both admission paths return when
// samePodActive hits, so the browser sees the same wording either way.
func errSamePodActive(namespace, pod string) error {
	return fmt.Errorf(
		"intercept: pod %s/%s already has an active interception; the agent binds fixed ports (30000, 30001, 15080) and programs a fixed nftables table, so only one interception can run per pod at a time — stop the existing session or target a different pod",
		namespace, pod)
}

// admit enforces the concurrency cap and the same-pod guard, then registers the
// session. The whole list -> check -> save runs under mu so two concurrent
// starts cannot both be admitted past the cap or into the same pod (no TOCTOU
// double-admit). It returns the fresh session id on success; on rejection it
// returns an error and registers nothing. cancel tears the session's context
// down when the session is later stopped by id.
func (r *webInterceptRegistry) admit(ctx context.Context, opts intercept.Options, cancel context.CancelFunc) (string, error) {
	id, err := newInterceptSessionID()
	if err != nil {
		return "", fmt.Errorf("intercept: mint session id: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	sessions, err := r.store.List(ctx)
	if err != nil {
		return "", fmt.Errorf("intercept: list active sessions: %w", err)
	}
	same, err := r.samePodActive(ctx, opts)
	if err != nil {
		return "", err
	}
	if same {
		return "", errSamePodActive(opts.Namespace, opts.Pod)
	}
	web := 0
	for _, s := range sessions {
		if isWebIntercept(s) {
			web++
		}
	}
	if web >= r.max {
		return "", fmt.Errorf("intercept: max concurrent sessions (%d) reached — stop an active session before starting another", r.max)
	}

	now := r.now()
	ps := intercept.PersistedSession{
		ID:        id,
		Actor:     opts.Actor,
		Cluster:   opts.Cluster,
		Namespace: opts.Namespace,
		Pod:       opts.Pod,
		Modes:     intercept.ModeStrings(opts.Modes),
		StartedAt: now,
		ExpiresAt: now.Add(r.ttl),
	}
	if err := r.store.Save(ctx, ps); err != nil {
		return "", fmt.Errorf("intercept: register session: %w", err)
	}
	r.cancels[id] = cancel
	return id, nil
}

// remove deregisters a session: it drops the in-process cancel func and
// deletes the store entry. It is the WebSocket-close teardown path and acts once
// per session; a delete racing the janitor's reap of the same just-lapsed entry
// is harmless (the store's delete is idempotent).
func (r *webInterceptRegistry) remove(ctx context.Context, id string) {
	r.mu.Lock()
	delete(r.cancels, id)
	r.mu.Unlock()
	if _, err := r.store.Delete(ctx, id); err != nil {
		zap.L().Warn("intercept: failed to deregister browser session", zap.String("session_id", shortInterceptID(id)), zap.Error(err))
	}
}

// refresh bumps a live entry's expiry so its lease never lapses while the
// session runs. It is a no-op once the session has been deregistered (its cancel
// func is gone), so a refresh racing teardown can never resurrect a deleted
// entry — teardown joins the heartbeat before it deletes, and this guard is the
// belt-and-suspenders backstop.
func (r *webInterceptRegistry) refresh(ctx context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, live := r.cancels[id]; !live {
		return nil
	}
	ps, ok, err := r.store.Get(ctx, id)
	if err != nil {
		return fmt.Errorf("intercept: get session: %w", err)
	}
	if !ok {
		return nil
	}
	ps.ExpiresAt = r.now().Add(r.ttl)
	if err := r.store.Save(ctx, ps); err != nil {
		return fmt.Errorf("intercept: refresh session lease: %w", err)
	}
	return nil
}

// heartbeat re-saves a live entry's lease on a ticker until ctx is cancelled
// (the WebSocket closed or the session ended). It is a single goroutine with a
// clear exit; the caller joins it before deregistering, so it is goleak-safe.
func (r *webInterceptRegistry) heartbeat(ctx context.Context, id string) {
	t := time.NewTicker(r.heartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Use a background context for the store write: the session context
			// is what schedules the loop, but a refresh already in flight should
			// finish rather than be aborted mid-write.
			if err := r.refresh(context.Background(), id); err != nil {
				zap.L().Warn("intercept: failed to refresh browser session lease", zap.String("session_id", shortInterceptID(id)), zap.Error(err))
			}
		}
	}
}

// stop cancels the live session with the given id and reports whether one was
// found. Cancellation tears the session's context down; the handler's own
// teardown then deregisters the entry. Unknown ids report false.
func (r *webInterceptRegistry) stop(id string) bool {
	r.mu.Lock()
	cancel, ok := r.cancels[id]
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// webInterceptView is the JSON shape of one active browser interception in the
// /sessions listing. It carries session/actor metadata only — never any secret
// or environment value.
type webInterceptView struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Namespace string    `json:"namespace"`
	Pod       string    `json:"pod"`
	Actor     string    `json:"actor"`
	Modes     []string  `json:"modes"`
	StartedAt time.Time `json:"started_at"`
}

// list returns the active browser interceptions (brokered entries in a shared
// store are filtered out), newest first is not guaranteed — the store's order.
func (r *webInterceptRegistry) list(ctx context.Context) ([]webInterceptView, error) {
	sessions, err := r.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]webInterceptView, 0, len(sessions))
	for _, s := range sessions {
		if !isWebIntercept(s) {
			continue
		}
		out = append(out, webInterceptView{
			ID:        s.ID,
			Cluster:   s.Cluster,
			Namespace: s.Namespace,
			Pod:       s.Pod,
			Actor:     s.Actor,
			Modes:     s.Modes,
			StartedAt: s.StartedAt,
		})
	}
	return out, nil
}

// reap deletes every orphaned browser-interception entry past its expiry and
// returns how many it removed. An entry reaches expiry only when its handler
// stopped heartbeating (it exited or crashed), so reaping just removes the stale
// registry record; the browser session's own teardown (WebSocket close -> ctx
// cancel) owns the agent, so there is no SIGTERM here. Only browser entries are
// touched (brokered entries are the Broker janitor's job), and an entry still
// live in this process is skipped even if its lease briefly lagged. It is the
// server-owned janitor's unit of work — it runs alongside any Broker janitor,
// which owns the brokered entries this one skips; exposed for a deterministic
// test.
func (r *webInterceptRegistry) reap(ctx context.Context) int {
	now := r.now()
	sessions, err := r.store.List(ctx)
	if err != nil {
		zap.L().Warn("intercept: browser-session janitor failed to list sessions; skipping this cycle", zap.Error(err))
		return 0
	}
	n := 0
	for _, s := range sessions {
		if !isWebIntercept(s) || !now.After(s.ExpiresAt) {
			continue
		}
		r.mu.Lock()
		_, live := r.cancels[s.ID]
		r.mu.Unlock()
		if live {
			continue
		}
		deleted, err := r.store.Delete(ctx, s.ID)
		if err != nil {
			zap.L().Warn("intercept: browser-session janitor failed to reap session; will retry next cycle",
				zap.String("session_id", shortInterceptID(s.ID)), zap.Error(err))
			continue
		}
		if deleted {
			n++
		}
	}
	return n
}

// startJanitor runs reap on a ticker until ctx is cancelled. Single goroutine,
// clear exit; goleak-safe. The returned channel closes once the goroutine has
// returned so callers (and tests) can wait for its exit deterministically.
func (r *webInterceptRegistry) startJanitor(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(r.heartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				r.reap(ctx)
			}
		}
	}()
	return done
}

// newInterceptSessionID returns an opaque, unguessable browser-interception
// session id.
func newInterceptSessionID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// shortInterceptID returns a log-safe prefix of a session id (ids are opaque
// CSPRNG values; the full id need not appear in logs).
func shortInterceptID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// handleInterceptSessions lists the active browser interception sessions for the
// UI. It is registered inside the session-token-authenticated /api/v1 group, so
// it inherits the same auth (and OPA gate) as the other UI endpoints and carries
// session metadata only.
// @Summary List active browser interception sessions
// @Tags intercept
// @Produce json
// @Success 200 {object} map[string][]webInterceptView
// @Router /api/v1/intercept/sessions [get]
// @Security BearerAuth
func (s *Server) handleInterceptSessions(w http.ResponseWriter, r *http.Request) {
	views := []webInterceptView{}
	if s.webIntercepts != nil {
		fallback, err := s.webIntercepts.list(r.Context())
		if err != nil {
			httpError(w, fmt.Errorf("list intercept sessions: %w", err), http.StatusInternalServerError)
			return
		}
		views = append(views, fallback...)
	}
	// Union in the tmux-backed resume sessions (empty when tmux is absent).
	for _, si := range tmuxListHoneyIntercept() {
		views = append(views, si.view())
	}
	writeJSON(w, map[string]any{"sessions": views})
}

// handleInterceptSessionStop cancels a live browser interception by id. Unknown
// ids report 404 (an already-closed session is indistinguishable from one that
// never existed). Same auth as handleInterceptSessions.
// @Summary Stop a browser interception session
// @Tags intercept
// @Param id path string true "session id"
// @Success 204
// @Failure 404 {object} map[string]string
// @Router /api/v1/intercept/sessions/{id}/stop [post]
// @Security BearerAuth
func (s *Server) handleInterceptSessionStop(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	// A honey-int-* id is a tmux resume session: kill it directly. Any other id
	// is a fallback-registry session cancelled by its context.
	if validInterceptMuxName(id) {
		if err := interceptResumeStop(id); err != nil {
			// A missing session is indistinguishable from one that never existed.
			http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if s.webIntercepts == nil || !s.webIntercepts.stop(id) {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
