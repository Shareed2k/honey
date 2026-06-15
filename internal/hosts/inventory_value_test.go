package hosts

import (
	"encoding/json"
	"testing"
)

func TestInventoryValueMarshalJSON(t *testing.T) {
	t.Parallel()
	record := Record{
		Name: "web-01",
		Vars: map[string]InventoryValue{
			"service":       MustInventoryValue("nginx"),
			"allow_restart": MustInventoryValue(true),
		},
	}
	b, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if got != `{"provider":"","name":"web-01","primary_ip":"","vars":{"allow_restart":true,"service":"nginx"}}` {
		t.Fatalf("got %s", got)
	}
}
