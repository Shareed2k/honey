package hosts

import "testing"

func TestMergeDedupe(t *testing.T) {
	a := []Record{
		{Provider: "aws", Name: "x", PrimaryIP: "1.1.1.1"},
		{Provider: "gcp", Name: "y", PrimaryIP: "2.2.2.2"},
	}
	b := []Record{
		{Provider: "aws", Name: "x", PrimaryIP: "1.1.1.1"},
		{Provider: "consul", Name: "z", PrimaryIP: "3.3.3.3"},
	}
	out := MergeDedupe(a, b)
	if len(out) != 3 {
		t.Fatalf("expected 3 unique rows, got %d", len(out))
	}
}

func TestDedupeKey(t *testing.T) {
	r := Record{Provider: "p", Name: "n", PrimaryIP: "10.0.0.1"}
	if DedupeKey(r) == "" {
		t.Fatal("empty key")
	}
}
