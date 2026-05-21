package ui

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestTableEnterAction(t *testing.T) {
	tests := []struct {
		name   string
		record hosts.Record
		want   action
	}{
		{
			name:   "truenas uses api shell",
			record: hosts.Record{Provider: "truenas", Meta: map[string]string{"kind": "vm"}},
			want:   actTrueNASAPI,
		},
		{
			name:   "aws uses ssh",
			record: hosts.Record{Provider: "aws", PrimaryIP: "10.0.0.1"},
			want:   actSSH,
		},
		{
			name:   "k8s pod uses ssh path",
			record: hosts.Record{Provider: "k8s", Meta: map[string]string{"kind": "pod"}},
			want:   actSSH,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tableEnterAction(tt.record); got != tt.want {
				t.Fatalf("tableEnterAction() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTruenasSSHKeyAllowed(t *testing.T) {
	tests := []struct {
		name   string
		record hosts.Record
		want   bool
	}{
		{
			name:   "truenas with ip",
			record: hosts.Record{Provider: "truenas", PrimaryIP: "192.168.1.10"},
			want:   true,
		},
		{
			name:   "truenas without ip",
			record: hosts.Record{Provider: "truenas"},
			want:   false,
		},
		{
			name:   "truenas whitespace ip",
			record: hosts.Record{Provider: "truenas", PrimaryIP: "  "},
			want:   false,
		},
		{
			name:   "non truenas ignored",
			record: hosts.Record{Provider: "aws", PrimaryIP: "10.0.0.1"},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truenasSSHKeyAllowed(tt.record); got != tt.want {
				t.Fatalf("truenasSSHKeyAllowed() = %v, want %v", got, tt.want)
			}
		})
	}
}
