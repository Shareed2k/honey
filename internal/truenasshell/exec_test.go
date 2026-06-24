package truenasshell

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestRecordSupportsAPIShell(t *testing.T) {
	if !RecordSupportsAPIShell(hosts.Record{Provider: "truenas", Meta: map[string]string{"kind": "vm", "virt_instance_id": "x"}}) {
		t.Fatal("expected vm supported")
	}
	if RecordSupportsAPIShell(hosts.Record{Provider: "aws", PrimaryIP: "1.2.3.4"}) {
		t.Fatal("expected aws false")
	}
}
