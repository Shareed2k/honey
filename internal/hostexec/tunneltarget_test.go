package hostexec

import "testing"

func TestParseTunnelTarget(t *testing.T) {
	t.Parallel()

	u, err := ParseTunnelTarget(FormatUnixTarget("/var/run/postgresql/.s.PGSQL.5432"))
	if err != nil {
		t.Fatalf("unix round-trip: %v", err)
	}
	if u.Scheme != TunnelUnix || u.Socket != "/var/run/postgresql/.s.PGSQL.5432" || u.Dest != u.Socket {
		t.Fatalf("unix parse: %+v", u)
	}

	if _, err := ParseTunnelTarget("unix:rel/path"); err == nil {
		t.Fatal("relative unix target must error")
	}

	tc, err := ParseTunnelTarget("10.0.0.5:5432")
	if err != nil {
		t.Fatalf("tcp parse err: %v", err)
	}
	if tc.Scheme != TunnelTCP || tc.Host != "10.0.0.5" || tc.Port != "5432" || tc.Dest != "10.0.0.5:5432" {
		t.Fatalf("tcp parse: %+v", tc)
	}
}
