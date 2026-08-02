package sshclient

import (
	"testing"

	"github.com/melbahja/goph"
)

// fakeResolver returns canned host configs by alias and counts calls per alias,
// so tests can drive resolveDialPlan without shelling out to `ssh -G`.
type fakeResolver struct {
	cfgs  map[string]*hostSSHConfig
	calls map[string]int
}

func newFakeResolver(cfgs map[string]*hostSSHConfig) *fakeResolver {
	return &fakeResolver{cfgs: cfgs, calls: map[string]int{}}
}

func (f *fakeResolver) resolve(alias, _ string) (*hostSSHConfig, error) {
	f.calls[alias]++
	c := f.cfgs[alias]
	return c, nil
}

func cfg(host string, port int, user, proxyJump string) *hostSSHConfig {
	return &hostSSHConfig{
		resolved:  resolvedSSH{host: host, port: port, user: user},
		proxyJump: proxyJump,
	}
}

// non-nil auth so resolveDialPlan skips buildAuthWithIdentityFiles (file I/O).
var testAuth = goph.Auth{}

func TestResolveDialPlan_direct(t *testing.T) {
	fr := newFakeResolver(map[string]*hostSSHConfig{
		"target": cfg("10.0.0.1", 22, "ops", ""),
	})
	plan, err := resolveDialPlan(fr.resolve, "", "target", 0, testAuth)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.hops) != 0 {
		t.Fatalf("hops = %d, want 0", len(plan.hops))
	}
	if plan.leaf.host != "10.0.0.1" || plan.leaf.port != 22 || plan.leaf.user != "ops" {
		t.Fatalf("leaf = %+v", plan.leaf)
	}
	if plan.leaf.addr() != "10.0.0.1:22" {
		t.Fatalf("addr = %q", plan.leaf.addr())
	}
}

func TestResolveDialPlan_portOverrideLeafOnly(t *testing.T) {
	fr := newFakeResolver(map[string]*hostSSHConfig{
		"target":  cfg("10.0.0.1", 22, "ops", "bastion"),
		"bastion": cfg("1.2.3.4", 22, "jump", ""),
	})
	plan, err := resolveDialPlan(fr.resolve, "", "target", 2222, testAuth)
	if err != nil {
		t.Fatal(err)
	}
	if plan.leaf.port != 2222 {
		t.Fatalf("leaf port = %d, want overridden 2222", plan.leaf.port)
	}
	if len(plan.hops) != 1 || plan.hops[0].port != 22 {
		t.Fatalf("hop port must stay 22 (override is leaf-only): %+v", plan.hops)
	}
}

func TestResolveDialPlan_proxyJumpChainOrderAndSingleResolve(t *testing.T) {
	fr := newFakeResolver(map[string]*hostSSHConfig{
		"target": cfg("10.0.0.1", 22, "ops", "b1,b2"),
		"b1":     cfg("1.1.1.1", 22, "u1", ""),
		"b2":     cfg("2.2.2.2", 2200, "u2", ""),
	})
	plan, err := resolveDialPlan(fr.resolve, "", "target", 0, testAuth)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.hops) != 2 {
		t.Fatalf("hops = %d, want 2", len(plan.hops))
	}
	if plan.hops[0].host != "1.1.1.1" || plan.hops[0].user != "u1" {
		t.Errorf("hop0 = %+v, want b1", plan.hops[0])
	}
	if plan.hops[1].host != "2.2.2.2" || plan.hops[1].port != 2200 || plan.hops[1].user != "u2" {
		t.Errorf("hop1 = %+v, want b2:2200", plan.hops[1])
	}
	if plan.leaf.host != "10.0.0.1" {
		t.Errorf("leaf = %+v", plan.leaf)
	}
	// arch-11: each host resolved exactly once (previously the chain was walked
	// twice — once to gather identities, once to dial).
	for alias, n := range fr.calls {
		if n != 1 {
			t.Errorf("resolve(%q) called %d times, want 1", alias, n)
		}
	}
}

func TestResolveDialPlan_hopSpecPortOverridesConfig(t *testing.T) {
	fr := newFakeResolver(map[string]*hostSSHConfig{
		"target":  cfg("10.0.0.1", 22, "ops", "bastion:9999"),
		"bastion": cfg("1.2.3.4", 22, "jump", ""),
	})
	plan, err := resolveDialPlan(fr.resolve, "", "target", 0, testAuth)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.hops) != 1 || plan.hops[0].port != 9999 {
		t.Fatalf("hop port must come from the jump spec (9999): %+v", plan.hops)
	}
}
