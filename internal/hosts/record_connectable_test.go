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
		{"truenas virt no ip", Record{Provider: "truenas", Meta: map[string]string{"kind": "virt_instance", "id": "inst-1"}}, true},
		{"truenas vm no ip", Record{Provider: "truenas", Name: "myvm", Meta: map[string]string{"kind": "vm"}}, true},
		{"truenas bad kind", Record{Provider: "truenas", Meta: map[string]string{"kind": "pool"}}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsConnectableRecord(tc.r); got != tc.want {
				t.Fatalf("IsConnectableRecord() = %v, want %v", got, tc.want)
			}
		})
	}
}
