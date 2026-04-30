package ui

import (
	"honey/internal/hosts"
	"testing"
)

func TestExecuteSSHParallel_emptyCmd(t *testing.T) {
	got, err := ExecuteSSHParallel("u", []hosts.Record{{Name: "a", PrimaryIP: "1.2.3.4"}}, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}
}

func TestExecuteSSHParallel_noIPs(t *testing.T) {
	got, err := ExecuteSSHParallel("u", []hosts.Record{{Name: "a", PrimaryIP: ""}}, "true", 0)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil slice, got %#v", got)
	}
}

func TestSortHostExecForUI_failuresFirst(t *testing.T) {
	in := []HostExecResult{
		{Name: "b", Success: true},
		{Name: "a", Success: false},
		{Name: "c", Success: false},
	}
	got := SortHostExecForUI(in)
	if len(got) != 3 {
		t.Fatal(len(got))
	}
	if got[0].Name != "a" || got[0].Success {
		t.Errorf("first want failed host a, got %+v", got[0])
	}
	if got[1].Name != "c" || got[1].Success {
		t.Errorf("second want failed host c, got %+v", got[1])
	}
	if got[2].Name != "b" || !got[2].Success {
		t.Errorf("third want ok host b, got %+v", got[2])
	}
}
