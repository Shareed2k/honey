package ui

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestIsExecutableHostDocker(t *testing.T) {
	if !isExecutableHost(hosts.Record{
		Provider: "docker",
		Meta:     map[string]string{"kind": "container", "container_id": "abc123"},
	}) {
		t.Fatal("expected docker container with container_id to be executable")
	}
	if isExecutableHost(hosts.Record{
		Provider: "docker",
		Meta:     map[string]string{"kind": "container"},
	}) {
		t.Fatal("expected docker row without container_id to be skipped")
	}
}
