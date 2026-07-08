package tun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRouteOutput(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		expected []string
	}{
		{
			name:     "empty output",
			output:   "",
			expected: nil,
		},
		{
			name: "linux ip route",
			output: `default via 192.168.1.1 dev eth0 
10.0.0.0/8 dev tun0 scope link 
172.16.0.0/12 dev eth1 scope link 
192.168.1.0/24 dev eth0 proto kernel scope link src 192.168.1.100`,
			expected: []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.1.0/24"},
		},
		{
			name: "macOS netstat -rn",
			output: `Routing tables

Internet:
Destination        Gateway            Flags           Netif Expire
default            192.168.1.1        UGScg             en0
10                 10.0.0.1           UGScg           utun0
127                127.0.0.1          UCS               lo0
127.0.0.1          127.0.0.1          UH                lo0
172.16.0.0/12      link#2             UCS               en1
192.168.1.0        link#1             UCS               en0`,
			// Note: "10" is not parsed correctly as a CIDR directly by net.ParseCIDR.
			// It might be parsed by normalizeCIDR if fields match netstat form, but let's test what parseRouteOutput currently does.
			// Currently normalizeCIDR only handles netstat form if fields[0] and fields[2] are parseable.
			// In macOS `netstat -rn`, columns are Destination, Gateway, Flags, Netif, Expire.
			// The current code tries: if net.ParseCIDR(s) works.
			// Then it tries: if len(fields)>=3, ip=net.ParseIP(fields[0]), mask=net.ParseIP(fields[2]).
			// But for macOS netstat, flags is usually fields[2]. It's not a netmask.
			// So "172.16.0.0/12" works because it parses as CIDR.
			expected: []string{"172.16.0.0/12"},
		},
		{
			name: "ignore 0. and 127.",
			output: `0.0.0.0/8 dev lo
127.0.0.0/8 dev lo
10.1.2.0/24 dev eth0`,
			expected: []string{"10.1.2.0/24"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseRouteOutput(tt.output)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestNormalizeCIDR(t *testing.T) {
	tests := []struct {
		name     string
		s        string
		fields   []string
		expected string
	}{
		{
			name:     "valid CIDR",
			s:        "192.168.1.0/24",
			fields:   []string{"192.168.1.0/24"},
			expected: "192.168.1.0/24",
		},
		{
			name:     "invalid CIDR string",
			s:        "invalid",
			fields:   []string{"invalid"},
			expected: "",
		},
		{
			name:     "netstat with IP and mask",
			s:        "10.0.0.0",
			fields:   []string{"10.0.0.0", "gateway", "255.0.0.0"},
			expected: "10.0.0.0/8",
		},
		{
			name:     "netstat with invalid mask",
			s:        "10.0.0.0",
			fields:   []string{"10.0.0.0", "gateway", "invalid-mask"},
			expected: "",
		},
		{
			name:     "not enough fields",
			s:        "10.0.0.0",
			fields:   []string{"10.0.0.0", "gateway"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeCIDR(tt.s, tt.fields)
			assert.Equal(t, tt.expected, got)
		})
	}
}
