package policy

import (
	"context"
	"testing"
)

func TestEnforcer_EmbeddedDefaultAllows(t *testing.T) {
	enf, err := New(context.Background(), "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, err := enf.Evaluate(context.Background(), map[string]any{"actor": "alice"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !d.Allow {
		t.Fatalf("embedded default should allow, got %+v", d)
	}
}

func TestEnforcer_FromSourceDeny(t *testing.T) {
	src := `package honey
import rego.v1
default allow := false
deny_reason := "blocked by test policy" if not allow
`
	enf, err := NewFromSource(context.Background(), "test.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	d, err := enf.Evaluate(context.Background(), map[string]any{"actor": "bob"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allow {
		t.Fatal("policy should deny")
	}
	if d.DenyReason != "blocked by test policy" {
		t.Fatalf("deny_reason = %q, want %q", d.DenyReason, "blocked by test policy")
	}
}

func TestEnforcer_ConditionalAllow(t *testing.T) {
	src := `package honey
import rego.v1
default allow := false
allow if input.actor == "ops"
`
	enf, err := NewFromSource(context.Background(), "cond.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	cases := []struct {
		actor string
		want  bool
	}{
		{"ops", true},
		{"dev", false},
	}
	for _, tc := range cases {
		d, err := enf.Evaluate(context.Background(), map[string]any{"actor": tc.actor})
		if err != nil {
			t.Fatalf("Evaluate(%q): %v", tc.actor, err)
		}
		if d.Allow != tc.want {
			t.Fatalf("actor %q: allow = %v, want %v", tc.actor, d.Allow, tc.want)
		}
	}
}
