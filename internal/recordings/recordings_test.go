package recordings

import (
	"encoding/json"
	"testing"
)

func TestValidateBaseName(t *testing.T) {
	if err := ValidateBaseName("20260101_120000_t_ssh_interactive_aws_host.hrec.jsonl"); err != nil {
		t.Fatalf("valid name: %v", err)
	}
	if err := ValidateBaseName("../x.hrec.jsonl"); err == nil {
		t.Fatal("expected error for traversal")
	}
	if err := ValidateBaseName("x.txt"); err == nil {
		t.Fatal("expected error for wrong suffix")
	}
}

func TestParseJSONL(t *testing.T) {
	raw := `{"time_ms":0,"type":"open","message":"trigger=t mode=m provider=p host=h ip=i user=u"}
{"time_ms":10,"type":"data","direction":"stdout","data_b64":"aGk="}
`
	evts, err := ParseJSONL([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(evts) != 2 || evts[0].Type != "open" || evts[1].Direction != "stdout" {
		t.Fatalf("unexpected: %#v", evts)
	}
	if HasStructuredBatch(evts) {
		t.Fatal("expected non-structured")
	}
}

func TestHasStructuredBatch(t *testing.T) {
	evts := []Event{{Type: "data", Direction: "stdout"}}
	if HasStructuredBatch(evts) {
		t.Fatal("stdout only")
	}
	evts = []Event{{Type: "result", Result: []byte(`{}`)}}
	if !HasStructuredBatch(evts) {
		t.Fatal("expected structured for result")
	}
	evts = []Event{{Type: "data", Direction: "plan"}}
	if !HasStructuredBatch(evts) {
		t.Fatal("expected structured for plan")
	}
}

func TestExtractFailedHosts(t *testing.T) {
	events := []Event{
		{
			Type:   "result",
			Result: json.RawMessage(`{"provider":"ssh", "name":"host1", "primary_ip":"1.1.1.1", "success":false}`),
		},
		{
			Type:   "result",
			Result: json.RawMessage(`{"provider":"ssh", "name":"host2", "primary_ip":"2.2.2.2", "success":true}`),
		},
	}

	failed := ExtractFailedHosts(events)
	if len(failed) != 1 || failed[0].Name != "host1" {
		t.Fatalf("expected 1 failed host 'host1', got %v", failed)
	}
}
