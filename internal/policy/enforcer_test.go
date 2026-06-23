package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEnforcer_EmbeddedDefaultAllows(t *testing.T) {
	enf, err := New(context.Background(), "", nil)
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

func TestEnforcer_DataInventory(t *testing.T) {
	// Policy decides from the injected data.inventory document, not from input.
	dir := t.TempDir()
	src := `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if data.inventory.vars.tier == "prod"
deny_reason := "prod tier blocked" if data.inventory.vars.tier == "prod"
`
	if err := os.WriteFile(filepath.Join(dir, "inv.rego"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	data := map[string]any{
		"inventory": map[string]any{
			"vars": map[string]any{"tier": "prod"},
		},
	}
	enf, err := New(context.Background(), dir, data)
	if err != nil {
		t.Fatalf("New with data: %v", err)
	}
	d, err := enf.Evaluate(context.Background(), map[string]any{"actor": "x"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allow {
		t.Fatalf("prod tier should be denied via data.inventory, got %+v", d)
	}
	if d.DenyReason != "prod tier blocked" {
		t.Fatalf("deny_reason = %q", d.DenyReason)
	}
}

func TestEnforcer_DecisionAndRequires(t *testing.T) {
	src := `package honey
import rego.v1
default allow := false
default deny_reason := "needs step-up"
decision := "require_biometric"
requires := ["explicit_approval", "biometric"]
`
	enf, err := NewFromSource(context.Background(), "d.rego", src)
	if err != nil {
		t.Fatalf("NewFromSource: %v", err)
	}
	d, err := enf.Evaluate(context.Background(), map[string]any{"actor": "alice"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if d.Allow {
		t.Fatal("expected deny")
	}
	if d.Decision != "require_biometric" {
		t.Fatalf("decision = %q", d.Decision)
	}
	if len(d.Requires) != 2 || d.Requires[0] != "explicit_approval" || d.Requires[1] != "biometric" {
		t.Fatalf("requires = %v", d.Requires)
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
