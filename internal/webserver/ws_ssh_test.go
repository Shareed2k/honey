package webserver

import (
	"encoding/json"
	"testing"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestWebSSHHelloResolvesDefaultSSHUserForPtyProxy(t *testing.T) {
	t.Parallel()
	s := &Server{opts: Options{Config: &config.File{Defaults: config.Defaults{SSHUser: "ops"}}}}
	helloIn := WSHello{
		SessionID: "abc",
		SSHUser:   "",
		Record:    hosts.Record{Provider: "local", Name: "vm", PrimaryIP: "10.0.0.1"},
		Cols:      80,
		Rows:      24,
	}
	rawIn, err := json.Marshal(helloIn)
	if err != nil {
		t.Fatal(err)
	}
	var hello WSHello
	if err := json.Unmarshal(rawIn, &hello); err != nil {
		t.Fatal(err)
	}
	user := s.sshUser(hello.SSHUser)
	hello.SSHUser = user
	rawOut, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	var decoded WSHello
	if err := json.Unmarshal(rawOut, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SSHUser != "ops" {
		t.Fatalf("pty-proxy payload ssh_user = %q, want ops", decoded.SSHUser)
	}
	if user != "ops" {
		t.Fatalf("resolved user = %q, want ops", user)
	}
}
