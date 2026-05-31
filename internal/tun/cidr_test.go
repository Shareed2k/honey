package tun

import (
	"net"
	"testing"
)

func TestComplementCIDRs_Empty(t *testing.T) {
	if got := ComplementCIDRs(nil); len(got) != 0 {
		t.Errorf("expected empty complement for nil nets, got %v", got)
	}
}

func TestComplementCIDRs_SingleNet(t *testing.T) {
	complements := ComplementCIDRs([]string{"10.0.0.0/8"})
	if len(complements) == 0 {
		t.Fatal("expected non-empty complement")
	}
	tunneled := net.ParseIP("10.1.2.3")
	public := net.ParseIP("8.8.8.8")
	var publicCovered bool
	for _, c := range complements {
		_, cidr, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatalf("complement produced invalid CIDR %q: %v", c, err)
		}
		if cidr.Contains(tunneled) {
			t.Errorf("complement %s contains 10.1.2.3 — should be in tunnel", c)
		}
		if cidr.Contains(public) {
			publicCovered = true
		}
	}
	if !publicCovered {
		t.Errorf("complement does not cover 8.8.8.8 — public traffic should bypass")
	}
}

func TestComplementCIDRs_MultipleNets(t *testing.T) {
	complements := ComplementCIDRs([]string{"10.0.0.0/8", "192.168.0.0/16"})
	check := []struct {
		ip      string
		inTunel bool
	}{
		{"10.5.5.5", true},
		{"192.168.1.1", true},
		{"8.8.8.8", false},
		{"172.16.0.1", false},
	}
	for _, tc := range check {
		ip := net.ParseIP(tc.ip)
		var inComplement bool
		for _, c := range complements {
			_, cidr, _ := net.ParseCIDR(c)
			if cidr.Contains(ip) {
				inComplement = true
			}
		}
		if tc.inTunel && inComplement {
			t.Errorf("%s should be in tunnel (not in complement), but complement covers it", tc.ip)
		}
		if !tc.inTunel && !inComplement {
			t.Errorf("%s should bypass (be in complement), but complement doesn't cover it", tc.ip)
		}
	}
}
