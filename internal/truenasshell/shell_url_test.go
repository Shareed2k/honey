package truenasshell

import "testing"

func TestShellWSURL(t *testing.T) {
	got, err := ShellWSURL("wss://nas.example.com/api/current")
	if err != nil {
		t.Fatal(err)
	}
	want := "wss://nas.example.com/websocket/shell"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestAPIWSURL_httpUpgradesToWSS(t *testing.T) {
	got, err := APIWSURL("http://192.168.1.1", true)
	if err != nil {
		t.Fatal(err)
	}
	if got != "wss://192.168.1.1/api/current" {
		t.Fatalf("got %q", got)
	}
}
