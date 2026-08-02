package plugins

import "testing"

// TestDockerConfigForRemote_CarriesInitMode proves dockerConfigForRemote
// carries the manifest's resolved init mode (and init_path) into the
// dockerTransportConfig it builds — without this, createContainer on the
// remote (DockerHostSession) path always saw InitMode=="" and silently fell
// back to bind mode regardless of what the manifest declared, defeating
// embedded mode's whole point of removing the host-shim dependency on remote
// docker hosts.
func TestDockerConfigForRemote_CarriesInitMode(t *testing.T) {
	lp := &loadedPlugin{
		manifest: Manifest{
			ID: "embedded-plugin",
			Docker: &DockerRuntime{
				Image:    "example.com/plugin@sha256:deadbeef",
				Init:     "embedded",
				InitPath: "/x",
			},
		},
	}

	cfg, err := dockerConfigForRemote(lp)
	if err != nil {
		t.Fatalf("dockerConfigForRemote: %v", err)
	}
	if cfg.InitMode != "embedded" {
		t.Fatalf("InitMode=%q want embedded", cfg.InitMode)
	}
	if cfg.InitPath != "/x" {
		t.Fatalf("InitPath=%q want /x", cfg.InitPath)
	}
}

// TestDockerConfigForRemote_DefaultsToBindMode proves the absent-Init case
// (the pre-existing, most common manifest shape) still resolves to explicit
// "bind" mode rather than staying "" — CallRaw's createContainer/
// entrypointForMode gate on the literal string "embedded", so any non-embedded
// value must normalize the same way the local loader
// (loadDockerPluginDir/manifest.Docker.effectiveInitMode) already does.
func TestDockerConfigForRemote_DefaultsToBindMode(t *testing.T) {
	lp := &loadedPlugin{
		manifest: Manifest{
			ID:     "bind-plugin",
			Docker: &DockerRuntime{Image: "example.com/plugin:latest"},
		},
	}

	cfg, err := dockerConfigForRemote(lp)
	if err != nil {
		t.Fatalf("dockerConfigForRemote: %v", err)
	}
	if cfg.InitMode != "bind" {
		t.Fatalf("InitMode=%q want bind", cfg.InitMode)
	}
	if cfg.InitPath != "" {
		t.Fatalf("InitPath=%q want empty in bind mode", cfg.InitPath)
	}
}

// TestDockerConfigForRemote_CarriesHostNetwork proves dockerConfigForRemote
// carries docker.network: "host" into the dockerTransportConfig it builds —
// without this, createContainer on the remote (DockerHostSession) path always
// saw HostNetwork==false and silently fell back to bridge mode + published
// ports regardless of what the manifest declared, defeating host-networking's
// whole point on a remote docker daemon.
func TestDockerConfigForRemote_CarriesHostNetwork(t *testing.T) {
	lp := &loadedPlugin{manifest: Manifest{Docker: &DockerRuntime{Image: "img", Network: "host"}}}
	cfg, err := dockerConfigForRemote(lp)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HostNetwork {
		t.Errorf("HostNetwork not carried to remote config")
	}
}

// TestDockerConfigForRemote_DefaultsHostNetworkFalse proves the absent/non-host
// docker.network case (the common manifest shape) resolves to HostNetwork:
// false rather than some zero-value ambiguity — mirrors the local loader's
// (loadDockerPluginDir) equivalent default.
func TestDockerConfigForRemote_DefaultsHostNetworkFalse(t *testing.T) {
	lp := &loadedPlugin{manifest: Manifest{Docker: &DockerRuntime{Image: "img"}}}
	cfg, err := dockerConfigForRemote(lp)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HostNetwork {
		t.Errorf("HostNetwork = true, want false when docker.network is unset")
	}
}
