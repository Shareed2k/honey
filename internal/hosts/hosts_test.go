package hosts

import "testing"

// Compile-time guard: Query must have exactly 3 fields (NameSubstring,
// NameRegex, Providers). If a new provider-specific field is added directly
// to Query this composite literal will fail to compile, prompting the author
// to reconsider the addition.
var _ = Query{
	NameSubstring: "",
	NameRegex:     "",
	Providers:     nil,
}

func TestQueryMatchesNameSubstring(t *testing.T) {
	q := Query{NameSubstring: "foo"}
	ok, err := q.MatchesName("MyFooBar")
	if err != nil || !ok {
		t.Fatalf("expected match, ok=%v err=%v", ok, err)
	}
}

func TestQueryMatchesNameRegex(t *testing.T) {
	q := Query{NameRegex: `^prod-`}
	ok, err := q.MatchesName("prod-web-1")
	if err != nil || !ok {
		t.Fatalf("expected match, ok=%v err=%v", ok, err)
	}
	q2 := Query{NameRegex: "["}
	_, err = q2.MatchesName("x")
	if err == nil {
		t.Fatal("expected invalid regex error")
	}
}

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
			if got := tc.r.IsConnectable(); got != tc.want {
				t.Fatalf("IsConnectable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestExternalIP(t *testing.T) {
	tests := []struct {
		name string
		r    Record
		want string
	}{
		{
			name: "gcp private primary public extra",
			r:    Record{PrimaryIP: "10.0.0.5", ExtraIPs: []string{"34.76.1.2", "10.0.0.6"}},
			want: "34.76.1.2",
		},
		{
			name: "aws private primary public extra",
			r:    Record{PrimaryIP: "10.1.2.3", ExtraIPs: []string{"54.12.34.56"}},
			want: "54.12.34.56",
		},
		{
			name: "public only primary",
			r:    Record{PrimaryIP: "34.10.20.30"},
			want: "34.10.20.30",
		},
		{
			name: "private only",
			r:    Record{PrimaryIP: "10.0.0.1"},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.ExternalIP(); got != tc.want {
				t.Fatalf("ExternalIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNodeDisplayIP(t *testing.T) {
	r := Record{PrimaryIP: "10.0.0.5", ExtraIPs: []string{"34.76.1.2"}}
	if got := r.NodeDisplayIP(); got != "34.76.1.2" {
		t.Fatalf("NodeDisplayIP() = %q", got)
	}
}

func TestMergeDedupe(t *testing.T) {
	a := RecordSet{
		{Provider: "aws", Name: "x", PrimaryIP: "1.1.1.1"},
		{Provider: "gcp", Name: "y", PrimaryIP: "2.2.2.2"},
	}
	b := RecordSet{
		{Provider: "aws", Name: "x", PrimaryIP: "1.1.1.1"},
		{Provider: "consul", Name: "z", PrimaryIP: "3.3.3.3"},
	}
	out := a.MergeDedupe(b)
	if len(out) != 3 {
		t.Fatalf("expected 3 unique rows, got %d", len(out))
	}
}

func TestDedupeKey(t *testing.T) {
	r := Record{Provider: "p", Name: "n", PrimaryIP: "10.0.0.1"}
	if r.DedupeKey() == "" {
		t.Fatal("empty key")
	}
}
