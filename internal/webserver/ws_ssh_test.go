package webserver

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestShouldUseWebPtyProxy(t *testing.T) {
	sshRec := hosts.Record{Provider: "gcp", Name: "vm1", PrimaryIP: "10.0.0.1"}
	dockerRec := hosts.Record{
		Provider: "docker",
		Meta:     map[string]string{"kind": "container", "container_id": "abc"},
	}
	k8sRec := hosts.Record{
		Provider: "k8s",
		Meta:     map[string]string{"kind": "pod", "namespace": "ns", "pod_name": "p"},
	}

	tests := []struct {
		name  string
		hello WSHello
		want  bool
	}{
		{"empty session", WSHello{Record: sshRec}, false},
		{"ssh with tab id", WSHello{SessionID: "tab-1", Record: sshRec}, true},
		{"docker with tab id", WSHello{SessionID: "tab-2", Record: dockerRec}, true},
		{"k8s pod with tab id", WSHello{SessionID: "tab-3", Record: k8sRec}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldUseWebPtyProxy(tc.hello); got != tc.want {
				t.Fatalf("shouldUseWebPtyProxy() = %v, want %v", got, tc.want)
			}
		})
	}
}
