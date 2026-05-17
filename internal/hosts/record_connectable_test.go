package hosts

import "testing"

func TestIsConnectableRecord(t *testing.T) {
	tests := []struct {
		name string
		r    Record
		want bool
	}{
		{"docker container", Record{Provider: "docker", Meta: map[string]string{"kind": "container", "container_id": "abc"}}, true},
		{"docker id only", Record{Provider: "docker", Meta: map[string]string{"container_id": "abc"}}, true},
		{"docker missing id", Record{Provider: "docker", Meta: map[string]string{"kind": "container"}}, false},
		{"vm ip", Record{Provider: "gcp", PrimaryIP: "10.0.0.1"}, true},
		{"k8s pod", Record{Provider: "k8s", Meta: map[string]string{"kind": "pod"}}, true},
		{"docker external ip only", Record{Provider: "docker", PrimaryIP: "34.1.2.3", Meta: map[string]string{"kind": "container", "container_id": "x"}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsConnectableRecord(tc.r); got != tc.want {
				t.Fatalf("IsConnectableRecord() = %v, want %v", got, tc.want)
			}
		})
	}
}
