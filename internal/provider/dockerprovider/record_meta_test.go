package dockerprovider

import "testing"

func TestRecordMetaBaseDockerSSHUser(t *testing.T) {
	bc := BackendConfig{SSHUser: "ubuntu", RunAs: "root"}
	meta := RecordMetaBase(bc, SSHHop{Host: "10.0.0.1"}, true)
	if meta["docker_ssh_user"] != "ubuntu" {
		t.Fatalf("docker_ssh_user = %q", meta["docker_ssh_user"])
	}
	if meta["docker_run_as"] != "root" {
		t.Fatalf("docker_run_as = %q", meta["docker_run_as"])
	}
	if meta["docker_discover"] != "1" {
		t.Fatalf("docker_discover = %q", meta["docker_discover"])
	}
}
