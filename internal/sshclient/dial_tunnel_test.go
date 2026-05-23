package sshclient

import (
	"context"
	"strings"
	"testing"
)

func TestRunTunnelGo_parseError(t *testing.T) {
	err := RunTunnelGo(context.Background(), "", "host", 0, "bad", nil)
	if err == nil || !strings.Contains(err.Error(), "tunnel spec") {
		t.Fatalf("got %v", err)
	}
}

func TestRunTunnelGo_emptyHost(t *testing.T) {
	err := RunTunnelGo(context.Background(), "", "  ", 0, "8080:h:80", nil)
	if err == nil || !strings.Contains(err.Error(), "no IP") {
		t.Fatalf("got %v", err)
	}
}
