package inventory

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestFilterRecordsByGroupAndVar(t *testing.T) {
	t.Parallel()
	records := []hosts.Record{
		{
			Name:   "web-01",
			Groups: []string{"prod", "web"},
			Vars: map[string]hosts.InventoryValue{
				"service": hosts.MustInventoryValue("nginx"),
			},
		},
		{
			Name:   "db-01",
			Groups: []string{"prod", "db"},
			Vars: map[string]hosts.InventoryValue{
				"service": hosts.MustInventoryValue("postgres"),
			},
		},
	}

	got, err := FilterRecords(records, []string{"group:web", "var:service=nginx"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "web-01" {
		t.Fatalf("got %#v", got)
	}
}

func TestFilterRecordsRejectsInvalidFilter(t *testing.T) {
	t.Parallel()
	_, err := FilterRecords(nil, []string{"var:missing_equals"})
	if err == nil {
		t.Fatal("expected error")
	}
}
