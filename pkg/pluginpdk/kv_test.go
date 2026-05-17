package pluginpdk

import (
	"encoding/json"
	"testing"
)

func TestParseKVOutput_ok(t *testing.T) {
	b, _ := json.Marshal(kvOutput{Found: true, Value: "v"})
	out, err := parseKVOutput(b)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || out.Value != "v" {
		t.Fatalf("got %+v", out)
	}
}

func TestParseKVOutput_hostError(t *testing.T) {
	b, _ := json.Marshal(kvOutput{Error: "kv not available for this call"})
	_, err := parseKVOutput(b)
	if err == nil || err.Error() != "kv not available for this call" {
		t.Fatalf("got err=%v", err)
	}
}

func TestParseKVOutput_invalidJSON(t *testing.T) {
	_, err := parseKVOutput([]byte("{"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestKVInputJSON(t *testing.T) {
	b, err := json.Marshal(kvInput{Op: "put", Key: "k", Value: "v"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded kvInput
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Op != "put" || decoded.Key != "k" || decoded.Value != "v" {
		t.Fatalf("got %+v", decoded)
	}
}
