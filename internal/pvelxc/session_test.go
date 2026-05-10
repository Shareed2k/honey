package pvelxc

import (
	"net/url"
	"strings"
	"testing"
)

func TestAPIBase(t *testing.T) {
	t.Parallel()
	if g, w := APIBase("https://x:8006"), "https://x:8006/api2/json"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
	if g, w := APIBase("https://x:8006/api2/json"), "https://x:8006/api2/json"; g != w {
		t.Fatalf("got %q want %q", g, w)
	}
}

func TestSerialWebSocketURL(t *testing.T) {
	t.Parallel()
	got, err := SerialWebSocketURL("https://pve.example:8006/api2/json", "my-node", "lxc", 105, "5900", "PVEVNC:abc")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if u.Scheme != "wss" || u.Host != "pve.example:8006" {
		t.Fatalf("host/scheme: %s %s", u.Scheme, u.Host)
	}
	if !strings.Contains(u.Path, "/nodes/my-node/lxc/105/vncwebsocket") {
		t.Fatalf("path: %s", u.Path)
	}
	q := u.Query()
	if q.Get("port") != "5900" || q.Get("vncticket") != "PVEVNC:abc" {
		t.Fatalf("query: %v", q)
	}

	gotQ, err := SerialWebSocketURL("https://pve.example:8006/api2/json", "my-node", "qemu", 201, "5901", "TICK")
	if err != nil {
		t.Fatal(err)
	}
	uq, err := url.Parse(gotQ)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(uq.Path, "/nodes/my-node/qemu/201/vncwebsocket") {
		t.Fatalf("qemu path: %s", uq.Path)
	}
}
