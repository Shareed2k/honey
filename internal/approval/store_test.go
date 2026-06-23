package approval

import (
	"testing"
	"time"
)

func TestStore_CreateGetDecide(t *testing.T) {
	s := NewStore(time.Hour)
	rec := s.Create("alice", "deploy.cue", []string{"h1"}, "high risk")
	if rec.Status != StatusPending {
		t.Fatalf("status = %q, want pending", rec.Status)
	}

	got, ok := s.Get(rec.ID)
	if !ok || got.Actor != "alice" {
		t.Fatalf("Get = %+v, ok=%v", got, ok)
	}

	decided, err := s.Decide(rec.ID, "bob", true)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decided.Status != StatusApproved || decided.Approver != "bob" {
		t.Fatalf("decided = %+v", decided)
	}

	// Second decision on an already-decided record errors.
	if _, err := s.Decide(rec.ID, "bob", true); err == nil {
		t.Fatal("expected error deciding twice")
	}
}

func TestStore_DecideMissing(t *testing.T) {
	s := NewStore(time.Hour)
	if _, err := s.Decide("nope", "bob", true); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestStore_Expiry(t *testing.T) {
	s := NewStore(time.Minute)
	base := time.Now()
	s.nowFn = func() time.Time { return base }
	rec := s.Create("alice", "r", nil, "")

	s.nowFn = func() time.Time { return base.Add(2 * time.Minute) }
	if _, ok := s.Get(rec.ID); ok {
		t.Fatal("expected record to expire")
	}
}
