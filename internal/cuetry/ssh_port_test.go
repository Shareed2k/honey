package cuetry

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestEffectiveSSHPort(t *testing.T) {
	t.Parallel()
	r := hosts.Record{Meta: map[string]string{"ssh_port": "2222"}}
	if p := EffectiveSSHPort(nil, &RemoteExec{}, r); p != 2222 {
		t.Fatalf("meta only got %d", p)
	}
	if p := EffectiveSSHPort(&RecipeDefaults{SSHPort: 3333}, &RemoteExec{}, r); p != 3333 {
		t.Fatalf("defaults over meta got %d", p)
	}
	if p := EffectiveSSHPort(&RecipeDefaults{SSHPort: 3333}, &RemoteExec{SSHPort: 4444}, r); p != 4444 {
		t.Fatalf("step over defaults got %d", p)
	}
	if p := EffectiveSSHPort(nil, &RemoteExec{SSHPort: 0}, hosts.Record{}); p != 0 {
		t.Fatalf("zero step got %d", p)
	}
}

func TestRecordForSSHDial(t *testing.T) {
	t.Parallel()
	r := hosts.Record{Name: "a", PrimaryIP: "10.0.0.1"}
	out := RecordForSSHDial(&RecipeDefaults{SSHPort: 2222, SSHPrivateKey: "/tmp/k"}, &RemoteExec{}, r)
	if p, ok := hosts.MetaSSHPort(&out); !ok || p != 2222 {
		t.Fatalf("port got %d ok=%v", p, ok)
	}
	if id, ok := hosts.MetaSSHIdentityFile(&out); !ok || id != "/tmp/k" {
		t.Fatalf("identity got %q ok=%v", id, ok)
	}
	if r.Meta != nil {
		t.Fatal("mutated input")
	}
}
