package jit

import (
	"encoding/base64"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// clock is a mutable, lock-protected now func for tests that need to
// fast-forward time deterministically.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *clock {
	return &clock{t: t}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newTestStore returns a Store backed by a fresh temp file plus the clock
// driving it.
func newTestStore(t *testing.T) (*Store, *clock) {
	t.Helper()
	c := newClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "grants.jsonl")
	s, err := NewStore(path, c.now)
	require.NoError(t, err)
	return s, c
}

// validGrant returns a baseline Grant that passes validateGrant; callers
// tweak fields as needed for their scenario.
func validGrant() Grant {
	return Grant{
		Actor:        "alice",
		Recipient:    "bob",
		Resource:     ResourceRef{Name: "host-1", Provider: "docker", PrimaryIP: "10.0.0.5"},
		Capabilities: []Capability{CapShell},
		Delivery:     DeliveryWeb,
		Duration:     time.Hour,
		Reason:       "debugging",
	}
}

func TestCreateActiveRedeem_HappyPath(t *testing.T) {
	s, c := newTestStore(t)

	stored, code, err := s.Create(validGrant())
	require.NoError(t, err)
	require.NotEmpty(t, stored.ID)
	require.NotEmpty(t, code)
	require.NotEmpty(t, stored.CodeHash)
	require.Equal(t, StatusApproved, stored.Status)
	require.Equal(t, c.now(), stored.StartsAt)
	require.Equal(t, c.now().Add(time.Hour), stored.ExpiresAt)
	require.True(t, s.Active(stored.ID))

	redeemed, err := s.Redeem(code)
	require.NoError(t, err)
	require.Equal(t, stored.ID, redeemed.ID)
	require.Equal(t, 1, redeemed.Redemptions)

	// Deep-copy guarantee: mutating the returned grant must not affect the store.
	redeemed.Capabilities[0] = CapExec
	got, ok := s.Get(stored.ID)
	require.True(t, ok)
	require.Equal(t, CapShell, got.Capabilities[0])
}

func TestCreate_RequireApprovalStartsPending(t *testing.T) {
	s, _ := newTestStore(t)

	g := validGrant()
	g.RequireApproval = true
	stored, code, err := s.Create(g)
	require.NoError(t, err)
	require.Equal(t, StatusPending, stored.Status)
	require.True(t, stored.StartsAt.IsZero())
	require.True(t, stored.ExpiresAt.IsZero())
	require.False(t, s.Active(stored.ID))

	_, err = s.Redeem(code)
	require.ErrorIs(t, err, ErrGrantNotActive)
}

func TestDecide_ApproveOpensWindowThenRedeemWorks(t *testing.T) {
	s, c := newTestStore(t)

	g := validGrant()
	g.RequireApproval = true
	stored, code, err := s.Create(g)
	require.NoError(t, err)

	decided, err := s.Decide(stored.ID, "carol", true)
	require.NoError(t, err)
	require.Equal(t, StatusApproved, decided.Status)
	require.Equal(t, "carol", decided.Approver)
	require.Equal(t, c.now(), decided.StartsAt)
	require.Equal(t, c.now().Add(g.Duration), decided.ExpiresAt)
	require.True(t, s.Active(stored.ID))

	redeemed, err := s.Redeem(code)
	require.NoError(t, err)
	require.Equal(t, 1, redeemed.Redemptions)
}

func TestDecide_DenyIsNotRedeemable(t *testing.T) {
	s, _ := newTestStore(t)

	g := validGrant()
	g.RequireApproval = true
	stored, code, err := s.Create(g)
	require.NoError(t, err)

	decided, err := s.Decide(stored.ID, "carol", false)
	require.NoError(t, err)
	require.Equal(t, StatusDenied, decided.Status)
	require.False(t, s.Active(stored.ID))

	_, err = s.Redeem(code)
	require.ErrorIs(t, err, ErrGrantNotActive)
}

func TestDecide_MissingGrant(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.Decide("jit_nope", "carol", true)
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestDecide_AlreadyDecidedErrors(t *testing.T) {
	s, _ := newTestStore(t)
	g := validGrant()
	g.RequireApproval = true
	stored, _, err := s.Create(g)
	require.NoError(t, err)

	_, err = s.Decide(stored.ID, "carol", true)
	require.NoError(t, err)

	_, err = s.Decide(stored.ID, "carol", true)
	require.ErrorIs(t, err, ErrGrantNotActive)
}

func TestRevoke_NotRedeemable(t *testing.T) {
	s, _ := newTestStore(t)

	stored, code, err := s.Create(validGrant())
	require.NoError(t, err)
	require.True(t, s.Active(stored.ID))

	revoked, err := s.Revoke(stored.ID, "dave")
	require.NoError(t, err)
	require.Equal(t, StatusRevoked, revoked.Status)
	require.Equal(t, "dave", revoked.Approver)
	require.False(t, s.Active(stored.ID))

	_, err = s.Redeem(code)
	require.ErrorIs(t, err, ErrGrantNotActive)
}

func TestRevoke_MissingOrTerminalErrors(t *testing.T) {
	s, _ := newTestStore(t)

	_, err := s.Revoke("jit_nope", "dave")
	require.ErrorIs(t, err, ErrGrantNotFound)

	stored, _, err := s.Create(validGrant())
	require.NoError(t, err)
	_, err = s.Revoke(stored.ID, "dave")
	require.NoError(t, err)

	_, err = s.Revoke(stored.ID, "dave")
	require.ErrorIs(t, err, ErrGrantNotActive)
}

func TestRedeem_RedemptionCap(t *testing.T) {
	s, _ := newTestStore(t)

	g := validGrant()
	g.MaxRedemptions = 1
	stored, code, err := s.Create(g)
	require.NoError(t, err)

	first, err := s.Redeem(code)
	require.NoError(t, err)
	require.Equal(t, 1, first.Redemptions)
	require.False(t, s.Active(stored.ID))

	_, err = s.Redeem(code)
	require.ErrorIs(t, err, ErrGrantNotActive)
}

func TestRedeem_Expiry(t *testing.T) {
	s, c := newTestStore(t)

	g := validGrant()
	g.Duration = time.Minute
	stored, code, err := s.Create(g)
	require.NoError(t, err)
	require.True(t, s.Active(stored.ID))

	c.advance(2 * time.Minute)
	require.False(t, s.Active(stored.ID))

	_, err = s.Redeem(code)
	require.ErrorIs(t, err, ErrGrantNotActive)
}

func TestRedeem_UnknownCode(t *testing.T) {
	s, _ := newTestStore(t)
	_, _, err := s.Create(validGrant())
	require.NoError(t, err)

	_, err = s.Redeem("this-code-does-not-exist")
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestRedeem_TamperedCodeSameLength(t *testing.T) {
	s, _ := newTestStore(t)
	_, code, err := s.Create(validGrant())
	require.NoError(t, err)

	raw, err := base64.RawURLEncoding.DecodeString(code)
	require.NoError(t, err)
	raw[0] ^= 0xFF // flip a byte, keep the length identical
	tampered := base64.RawURLEncoding.EncodeToString(raw)
	require.NotEqual(t, code, tampered)
	require.Equal(t, len(code), len(tampered))

	_, err = s.Redeem(tampered)
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestCreate_ValidationFailures(t *testing.T) {
	tests := map[string]func(g *Grant){
		"zero duration": func(g *Grant) {
			g.Duration = 0
		},
		"no capabilities": func(g *Grant) {
			g.Capabilities = nil
		},
		"invalid capability": func(g *Grant) {
			g.Capabilities = []Capability{"sudo"}
		},
		"bad delivery": func(g *Grant) {
			g.Delivery = "carrier-pigeon"
		},
		"empty resource name": func(g *Grant) {
			g.Resource.Name = ""
		},
		"negative max redemptions": func(g *Grant) {
			g.MaxRedemptions = -1
		},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s, _ := newTestStore(t)
			g := validGrant()
			mutate(&g)

			_, _, err := s.Create(g)
			require.Error(t, err)
			require.ErrorIs(t, err, ErrInvalidGrant)
		})
	}
}

// TestJITLiveTerminalGrant_Validation table-drives Store.Create's decidable
// half of the live_terminal shape check (validateLiveTerminal): the full
// mux-session-name validation lives in the webserver grant-create handler
// (this package cannot import it without a cycle), so here mux_session is
// only checked for non-emptiness.
func TestJITLiveTerminalGrant_Validation(t *testing.T) {
	liveGrant := func(caps []Capability, muxSession string) Grant {
		g := validGrant()
		g.Capabilities = caps
		g.Resource.Meta = map[string]string{"kind": "live_terminal"}
		if muxSession != "" {
			g.Resource.Meta["mux_session"] = muxSession
		}
		return g
	}

	tests := map[string]struct {
		grant   Grant
		wantErr bool
	}{
		"watch is valid": {
			grant: liveGrant([]Capability{CapWatch}, "honey_abc123"),
		},
		"collaborate is valid": {
			grant: liveGrant([]Capability{CapCollab}, "honey-int-deadbeef"),
		},
		"reject both watch and collaborate": {
			grant:   liveGrant([]Capability{CapWatch, CapCollab}, "honey_abc123"),
			wantErr: true,
		},
		"reject neither watch nor collaborate": {
			grant:   liveGrant([]Capability{CapShell}, "honey_abc123"),
			wantErr: true,
		},
		"reject empty mux_session": {
			grant:   liveGrant([]Capability{CapWatch}, ""),
			wantErr: true,
		},
		"reject watch capability outside live_terminal kind": {
			grant: func() Grant {
				g := validGrant()
				g.Capabilities = []Capability{CapWatch}
				return g
			}(),
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s, _ := newTestStore(t)
			stored, code, err := s.Create(tc.grant)
			if tc.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, ErrInvalidGrant)
				return
			}
			require.NoError(t, err)
			require.NotEmpty(t, code)
			require.Equal(t, tc.grant.Resource.Meta["mux_session"], stored.Resource.Meta["mux_session"])
		})
	}
}

func TestPersistence_RoundTrip(t *testing.T) {
	c := newClock(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC))
	path := filepath.Join(t.TempDir(), "grants.jsonl")

	s1, err := NewStore(path, c.now)
	require.NoError(t, err)

	direct, _, err := s1.Create(validGrant())
	require.NoError(t, err)

	pendingGrant := validGrant()
	pendingGrant.Resource.Name = "host-2"
	pendingGrant.Resource.Meta = map[string]string{"env": "staging"}
	pendingGrant.RequireApproval = true
	pending, _, err := s1.Create(pendingGrant)
	require.NoError(t, err)

	revokedGrant := validGrant()
	revokedGrant.Resource.Name = "host-3"
	revokedSrc, _, err := s1.Create(revokedGrant)
	require.NoError(t, err)
	revoked, err := s1.Revoke(revokedSrc.ID, "dave")
	require.NoError(t, err)

	// Reopen a NEW store on the same path and same clock.
	s2, err := NewStore(path, c.now)
	require.NoError(t, err)

	gotDirect, ok := s2.Get(direct.ID)
	require.True(t, ok)
	require.Equal(t, direct, gotDirect)

	gotPending, ok := s2.Get(pending.ID)
	require.True(t, ok)
	require.Equal(t, pending, gotPending)
	require.Equal(t, "staging", gotPending.Resource.Meta["env"])

	gotRevoked, ok := s2.Get(revoked.ID)
	require.True(t, ok)
	require.Equal(t, revoked, gotRevoked)

	list := s2.List()
	require.Len(t, list, 3)
}

func TestPersistence_MissingFileYieldsEmptyStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist-yet", "grants.jsonl")
	s, err := NewStore(path, nil)
	require.NoError(t, err)
	require.Empty(t, s.List())
}

func TestGC_DropsOldTerminalGrant(t *testing.T) {
	s, c := newTestStore(t)

	// A short Duration keeps ExpiresAt close to CreatedAt so the GC reference
	// time (the later of CreatedAt/ExpiresAt/DecidedAt) is dominated by the
	// near-immediate revocation rather than a far-future ExpiresAt.
	g := validGrant()
	g.Duration = time.Minute
	stored, _, err := s.Create(g)
	require.NoError(t, err)
	_, err = s.Revoke(stored.ID, "dave")
	require.NoError(t, err)

	// Past retention, measured from ExpiresAt (the later reference here).
	c.advance(retention + 2*time.Minute)

	_, ok := s.Get(stored.ID)
	require.False(t, ok, "expected revoked grant older than retention to be GC'd")
	require.Empty(t, s.List())
}

func TestGC_KeepsRecentTerminalGrant(t *testing.T) {
	s, c := newTestStore(t)

	g := validGrant()
	g.Duration = time.Minute
	stored, _, err := s.Create(g)
	require.NoError(t, err)
	_, err = s.Revoke(stored.ID, "dave")
	require.NoError(t, err)

	// Just under retention past ExpiresAt (the later reference time).
	c.advance(retention + time.Minute - time.Second)

	_, ok := s.Get(stored.ID)
	require.True(t, ok, "expected recently revoked grant to survive GC")
}

func TestConcurrentCreateAndRedeem(t *testing.T) {
	s, _ := newTestStore(t)

	const n = 20
	codes := make([]string, n)
	for i := 0; i < n; i++ {
		_, code, err := s.Create(validGrant())
		require.NoError(t, err)
		codes[i] = code
	}

	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for _, code := range codes {
		wg.Add(2)
		go func(code string) {
			defer wg.Done()
			_, err := s.Redeem(code)
			errs <- err
		}(code)
		go func() {
			defer wg.Done()
			s.List()
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
}

func TestPeek_ApprovedGrant(t *testing.T) {
	s, _ := newTestStore(t)

	stored, code, err := s.Create(validGrant())
	require.NoError(t, err)

	peeked, err := s.Peek(code)
	require.NoError(t, err)
	require.Equal(t, stored.ID, peeked.ID)
	require.Equal(t, StatusApproved, peeked.Status)
	require.Equal(t, 0, peeked.Redemptions)

	// Peek must not consume a redemption or otherwise mutate the grant.
	got, ok := s.Get(stored.ID)
	require.True(t, ok)
	require.Equal(t, 0, got.Redemptions)
	require.Equal(t, stored, got)
}

func TestPeek_PendingGrant(t *testing.T) {
	s, _ := newTestStore(t)

	g := validGrant()
	g.RequireApproval = true
	stored, code, err := s.Create(g)
	require.NoError(t, err)

	peeked, err := s.Peek(code)
	require.NoError(t, err)
	require.Equal(t, stored.ID, peeked.ID)
	require.Equal(t, StatusPending, peeked.Status)
}

func TestPeek_UnknownCode(t *testing.T) {
	s, _ := newTestStore(t)
	_, _, err := s.Create(validGrant())
	require.NoError(t, err)

	_, err = s.Peek("this-code-does-not-exist")
	require.ErrorIs(t, err, ErrGrantNotFound)
}

func TestPeek_DoesNotConsumeRedemption(t *testing.T) {
	s, _ := newTestStore(t)

	stored, code, err := s.Create(validGrant())
	require.NoError(t, err)

	// Multiple peeks must never increment Redemptions.
	for i := 0; i < 3; i++ {
		_, err := s.Peek(code)
		require.NoError(t, err)
	}

	got, ok := s.Get(stored.ID)
	require.True(t, ok)
	require.Equal(t, 0, got.Redemptions)

	// A real Redeem afterward still works normally (peeking didn't cap it).
	redeemed, err := s.Redeem(code)
	require.NoError(t, err)
	require.Equal(t, 1, redeemed.Redemptions)
}

func TestErrors_AreSentinelsNotJustStrings(t *testing.T) {
	require.True(t, errors.Is(ErrGrantNotFound, ErrGrantNotFound))
	require.True(t, errors.Is(ErrGrantNotActive, ErrGrantNotActive))
	require.True(t, errors.Is(ErrGrantNotTerminal, ErrGrantNotTerminal))
	require.True(t, errors.Is(ErrInvalidGrant, ErrInvalidGrant))
}

// TestDelete_RefusesActiveGrant is the load-bearing Delete invariant: an
// ACTIVE grant (pending, or approved and still within its window) must never
// be deletable directly — the operator has to revoke (or kill, for a
// live-terminal share) it first, so a delete can never silently drop a live
// share's audit trail before it actually ends.
func TestDelete_RefusesActiveGrant(t *testing.T) {
	tests := []struct {
		name            string
		requireApproval bool
	}{
		{name: "approved and within window", requireApproval: false},
		{name: "pending", requireApproval: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newTestStore(t)
			g := validGrant()
			g.RequireApproval = tc.requireApproval
			stored, _, err := s.Create(g)
			require.NoError(t, err)

			err = s.Delete(stored.ID)
			require.ErrorIs(t, err, ErrGrantNotTerminal)

			_, ok := s.Get(stored.ID)
			require.True(t, ok, "an active grant must survive a refused delete")
		})
	}
}

// TestDelete_RemovesTerminalGrant covers every terminal status: denied,
// revoked, and approved-but-expired all become deletable, and Delete makes
// them actually disappear (not just report success).
func TestDelete_RemovesTerminalGrant(t *testing.T) {
	tests := []struct {
		name    string
		makeErr func(s *Store, c *clock, id string) error
	}{
		{
			name: "revoked",
			makeErr: func(s *Store, _ *clock, id string) error {
				_, err := s.Revoke(id, "dave")
				return err
			},
		},
		{
			name: "denied",
			makeErr: func(s *Store, _ *clock, id string) error {
				_, err := s.Decide(id, "dave", false)
				return err
			},
		},
		{
			name: "expired",
			makeErr: func(_ *Store, c *clock, _ string) error {
				c.advance(2 * time.Hour) // past the 1h Duration in validGrant
				return nil
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, c := newTestStore(t)
			g := validGrant()
			if tc.name == "denied" {
				g.RequireApproval = true
			}
			stored, _, err := s.Create(g)
			require.NoError(t, err)
			require.NoError(t, tc.makeErr(s, c, stored.ID))

			require.NoError(t, s.Delete(stored.ID))
			_, ok := s.Get(stored.ID)
			require.False(t, ok, "deleted grant must be gone")
		})
	}
}

func TestDelete_MissingGrant(t *testing.T) {
	s, _ := newTestStore(t)
	err := s.Delete("jit_nope")
	require.ErrorIs(t, err, ErrGrantNotFound)
}

// TestPurge_DeletesOnlyTerminalGrants is the bulk "delete all finished
// grants" path: it must remove every terminal grant, return their count, and
// leave every active grant untouched.
func TestPurge_DeletesOnlyTerminalGrants(t *testing.T) {
	s, _ := newTestStore(t)

	active, _, err := s.Create(validGrant())
	require.NoError(t, err)

	revoked, _, err := s.Create(validGrant())
	require.NoError(t, err)
	_, err = s.Revoke(revoked.ID, "dave")
	require.NoError(t, err)

	denied := validGrant()
	denied.RequireApproval = true
	deniedGrant, _, err := s.Create(denied)
	require.NoError(t, err)
	_, err = s.Decide(deniedGrant.ID, "dave", false)
	require.NoError(t, err)

	n, err := s.Purge()
	require.NoError(t, err)
	require.Equal(t, 2, n, "expected exactly the revoked and denied grants purged")

	_, ok := s.Get(active.ID)
	require.True(t, ok, "an active grant must survive Purge")
	_, ok = s.Get(revoked.ID)
	require.False(t, ok)
	_, ok = s.Get(deniedGrant.ID)
	require.False(t, ok)
}

func TestPurge_NoTerminalGrantsReturnsZero(t *testing.T) {
	s, _ := newTestStore(t)
	_, _, err := s.Create(validGrant())
	require.NoError(t, err)

	n, err := s.Purge()
	require.NoError(t, err)
	require.Equal(t, 0, n)
	require.Len(t, s.List(), 1)
}
