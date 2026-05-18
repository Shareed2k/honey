package dockerprovider

import (
	"strings"
	"testing"
)

func TestRunAsProxyCommandUsesDockerDialStdio(t *testing.T) {
	t.Parallel()
	got := RunAsProxyCommandForTest("root", "/var/run/docker.sock")
	if strings.Contains(got, "socat") {
		t.Fatalf("must not use socat: %q", got)
	}
	if !strings.Contains(got, "system dial-stdio") {
		t.Fatalf("missing dial-stdio: %q", got)
	}
	if !strings.Contains(got, "unix:///var/run/docker.sock") {
		t.Fatalf("missing docker host: %q", got)
	}
}

func TestDockerHostFlag(t *testing.T) {
	t.Parallel()
	if got := dockerHostFlag("/var/run/docker.sock"); got != "unix:///var/run/docker.sock" {
		t.Fatalf("got %q", got)
	}
	if got := dockerHostFlag("unix:///custom.sock"); got != "unix:///custom.sock" {
		t.Fatalf("got %q", got)
	}
}
