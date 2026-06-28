package config

import "testing"

func TestPluginsWithDefaults_networkDenyDefaultTrue(t *testing.T) {
	t.Parallel()
	// An unset network_deny must default to true (secure-by-default).
	e := Plugins{}.WithDefaults()
	if !e.NetworkDeny {
		t.Fatal("NetworkDeny default must be true; got false")
	}
}

func TestPluginsWithDefaults_networkDenyExplicitFalse(t *testing.T) {
	t.Parallel()
	f := false
	e := Plugins{NetworkDeny: &f}.WithDefaults()
	if e.NetworkDeny {
		t.Fatal("explicit NetworkDeny=false must be honoured")
	}
}

func TestPluginsWithDefaults_networkDenyExplicitTrue(t *testing.T) {
	t.Parallel()
	tr := true
	e := Plugins{NetworkDeny: &tr}.WithDefaults()
	if !e.NetworkDeny {
		t.Fatal("explicit NetworkDeny=true must be honoured")
	}
}
