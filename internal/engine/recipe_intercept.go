package engine

import (
	"fmt"
	"sync"
)

// interceptCoordCloseReason is the audit-visible stop reason recorded for
// every session still registered when the coordinator tears down at run end
// (as opposed to a step-level failure, which never reaches Close directly —
// establishing steps stay registered even after their own command finishes).
const interceptCoordCloseReason = "recipe run complete"

// RecipeInterceptCoordinator tracks the intercept sessions established by one
// cue-exec run so a later session_step step can reuse the same deployed
// agent, and tears every session down exactly once at run end. Unlike
// RecipeTunnelCoordinator it has no pool/Acquire: an intercept step
// establishes its session explicitly (deploying its own agent), it never
// shares one across steps the way a tunnel is shared across hosts.
type RecipeInterceptCoordinator struct {
	mu       sync.Mutex
	sessions map[string]interceptLive // stepID -> live session
	order    []string                 // registration order, for deterministic reverse Close
	closed   bool
}

// NewRecipeInterceptCoordinator creates an empty, run-scoped coordinator.
func NewRecipeInterceptCoordinator() *RecipeInterceptCoordinator {
	return &RecipeInterceptCoordinator{sessions: make(map[string]interceptLive)}
}

// Count reports how many sessions are currently registered, so an
// establishing step can enforce the config's max_sessions cap before
// deploying another agent.
func (c *RecipeInterceptCoordinator) Count() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.sessions)
}

// Register stores live under stepID for a later session_step lookup and
// keeps it alive until the coordinator's Close tears every session down at
// run end — an establishing intercept step registers its Live but must never
// Close it itself. If the coordinator is nil or already closed, live is
// closed immediately instead of being retained, so a race with run teardown
// never leaves an agent deployed past the point the run believes everything
// is torn down.
//
// maxSessions enforces the config's max_sessions cap atomically with
// registration: when maxSessions > 0 and the coordinator already holds
// maxSessions sessions, Register rejects live (closing neither it nor any
// existing session) and returns an error instead of adding it — the caller
// closes the just-established live. This is what makes the cap race-safe
// across a graph wave's concurrent establishing steps: a
// Count()-then-Register check from the caller would be a check-then-act
// race, since two steps could both pass Count() before either registers.
func (c *RecipeInterceptCoordinator) Register(stepID string, live interceptLive, maxSessions int) error {
	if live == nil {
		return nil
	}
	if c == nil {
		live.Close(interceptCoordCloseReason)
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		live.Close(interceptCoordCloseReason)
		return nil
	}
	if maxSessions > 0 && len(c.sessions) >= maxSessions {
		return fmt.Errorf("intercept: max_sessions (%d) reached", maxSessions)
	}
	c.sessions[stepID] = live
	c.order = append(c.order, stepID)
	return nil
}

// Lookup returns the live session registered under stepID, for a
// session_step reference on a later step.
func (c *RecipeInterceptCoordinator) Lookup(stepID string) (interceptLive, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	live, ok := c.sessions[stepID]
	return live, ok
}

// Close tears down every registered session exactly once, in reverse
// registration order. It is idempotent: a second call is a no-op.
func (c *RecipeInterceptCoordinator) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	for i := len(c.order) - 1; i >= 0; i-- {
		id := c.order[i]
		live, ok := c.sessions[id]
		if !ok {
			continue
		}
		delete(c.sessions, id)
		live.Close(interceptCoordCloseReason)
	}
	c.order = nil
}
