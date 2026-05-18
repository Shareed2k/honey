package hosts

import "testing"

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
			if got := ExternalIP(tc.r); got != tc.want {
				t.Fatalf("ExternalIP() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNodeDisplayIP(t *testing.T) {
	r := Record{PrimaryIP: "10.0.0.5", ExtraIPs: []string{"34.76.1.2"}}
	if got := NodeDisplayIP(r); got != "34.76.1.2" {
		t.Fatalf("NodeDisplayIP() = %q", got)
	}
}
