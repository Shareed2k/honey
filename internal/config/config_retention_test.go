package config

import (
	"testing"
	"time"
)

func TestParseRetentionDuration(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		ok   bool
		days int
	}{
		{"", true, 0},
		{"0", true, 0},
		{"30d", true, 30},
		{"720h", true, 0},
		{"badx", false, 0},
	}
	for _, tc := range cases {
		d, err := ParseRetentionDuration(tc.in)
		if tc.ok && err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%q: expected error", tc.in)
		}
		if tc.days > 0 && d != 30*24*time.Hour {
			t.Fatalf("%q: got %v", tc.in, d)
		}
	}
}
