package truenasshell

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestShellOptionsSupported(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		rec     hosts.Record
		wantErr bool
	}{
		{name: "appliance", rec: hosts.Record{Meta: map[string]string{"kind": "appliance"}}},
		{name: "virt", rec: hosts.Record{Meta: map[string]string{"kind": "virt_instance", "id": "abc"}}},
		{name: "vm with virt id", rec: hosts.Record{Name: "vm1", Meta: map[string]string{"kind": "vm", "virt_instance_id": "inst-1"}}},
		{name: "vm with name", rec: hosts.Record{Name: "vm1", Meta: map[string]string{"kind": "vm", "vm_id": "1"}}},
		{name: "vm missing name", rec: hosts.Record{Meta: map[string]string{"kind": "vm"}}, wantErr: true},
		{name: "virt missing id", rec: hosts.Record{Meta: map[string]string{"kind": "virt_instance"}}, wantErr: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := shellOptionsSupported(tc.rec)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestResolveShellOptions_static(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		rec     hosts.Record
		wantNil bool
		wantKey string
		wantVal any
	}{
		{
			name:    "appliance",
			rec:     hosts.Record{Meta: map[string]string{"kind": "appliance"}},
			wantNil: true,
		},
		{
			name:    "virt",
			rec:     hosts.Record{Meta: map[string]string{"kind": "virt_instance", "id": "abc"}},
			wantKey: "virt_instance_id",
			wantVal: "abc",
		},
		{
			name:    "vm cached virt id",
			rec:     hosts.Record{Name: "vm1", Meta: map[string]string{"kind": "vm", "virt_instance_id": "inst-9"}},
			wantKey: "virt_instance_id",
			wantVal: "inst-9",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			opts, err := resolveShellOptions(t.Context(), nil, tc.rec)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantNil {
				if opts != nil {
					t.Fatalf("expected nil options, got %#v", opts)
				}
				return
			}
			if opts[tc.wantKey] != tc.wantVal {
				t.Fatalf("opts[%q]=%#v want %#v", tc.wantKey, opts[tc.wantKey], tc.wantVal)
			}
		})
	}
}
