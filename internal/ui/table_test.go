package ui

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestParallelExecTargets_noMarksAllWithIP(t *testing.T) {
	m := &model{
		recs: []hosts.Record{
			{Name: "a", PrimaryIP: "1.1.1.1"},
			{Name: "b", PrimaryIP: ""},
			{Name: "c", PrimaryIP: "3.3.3.3"},
		},
		selected: make(map[int]struct{}),
	}
	got, note := m.parallelExecTargets()
	if len(got) != 2 {
		t.Fatalf("want 2 hosts, got %d: %+v", len(got), got)
	}
	if note == "" {
		t.Fatal("expected scope note")
	}
}

func TestParallelExecTargets_marksLimitHosts(t *testing.T) {
	m := &model{
		recs: []hosts.Record{
			{Name: "a", PrimaryIP: "1.1.1.1"},
			{Name: "b", PrimaryIP: "2.2.2.2"},
			{Name: "c", PrimaryIP: "3.3.3.3"},
		},
		selected: map[int]struct{}{1: {}},
	}
	got, note := m.parallelExecTargets()
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("got %+v", got)
	}
	if note == "" {
		t.Fatal("expected scope note")
	}
}

func TestParallelExecTargets_markedNoIPSkipped(t *testing.T) {
	m := &model{
		recs: []hosts.Record{
			{Name: "a", PrimaryIP: ""},
			{Name: "b", PrimaryIP: "2.2.2.2"},
		},
		selected: map[int]struct{}{0: {}, 1: {}},
	}
	got, _ := m.parallelExecTargets()
	if len(got) != 1 || got[0].Name != "b" {
		t.Fatalf("got %+v", got)
	}
}
