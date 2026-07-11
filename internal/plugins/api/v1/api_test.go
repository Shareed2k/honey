package v1

import (
	"encoding/json"
	"testing"
)

func TestExecRequestResponse_JSONRoundTrip(t *testing.T) {
	req := ExecRequest{Argv: []string{"echo", "hi"}, Env: map[string]string{"DB_PASSWORD": "s3cr3t"}, Stdin: []byte("in")}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ExecRequest
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Argv[0] != "echo" || got.Argv[1] != "hi" || string(got.Stdin) != "in" {
		t.Fatalf("got=%+v", got)
	}
	if got.Env["DB_PASSWORD"] != "s3cr3t" {
		t.Fatalf("got.Env=%v", got.Env)
	}

	resp := ExecResponse{Output: "hi\n", ExitCode: 0}
	b, err = json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var gotResp ExecResponse
	if err := json.Unmarshal(b, &gotResp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if gotResp.Output != "hi\n" {
		t.Fatalf("gotResp=%+v", gotResp)
	}
}
