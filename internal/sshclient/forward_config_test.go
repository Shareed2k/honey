package sshclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOpenSSHGForwards_sample(t *testing.T) {
	const sample = `host testhost
localforward 8888 [127.0.0.1]:8000
localforward [::1]:9090 [db.internal]:5432
remoteforward 2222 [localhost]:22
dynamicforward 1080
dynamicforward [127.0.0.1]:1081
`
	set := ParseOpenSSHGForwards([]byte(sample))
	if len(set.Local) != 2 {
		t.Fatalf("locals: %d", len(set.Local))
	}
	if set.Local[0].BindPort != 8888 || set.Local[0].RemoteHost != "127.0.0.1" || set.Local[0].RemotePort != 8000 {
		t.Fatalf("local[0]: %+v", set.Local[0])
	}
	if set.Local[1].BindHost != "::1" || set.Local[1].BindPort != 9090 || set.Local[1].RemoteHost != "db.internal" || set.Local[1].RemotePort != 5432 {
		t.Fatalf("local[1]: %+v", set.Local[1])
	}
	if len(set.Remote) != 1 || set.Remote[0].BindPort != 2222 || set.Remote[0].LocalHost != "localhost" || set.Remote[0].LocalPort != 22 {
		t.Fatalf("remote: %+v", set.Remote)
	}
	if len(set.Dynamic) != 2 || set.Dynamic[0].BindPort != 1080 || set.Dynamic[1].BindHost != "127.0.0.1" || set.Dynamic[1].BindPort != 1081 {
		t.Fatalf("dynamic: %+v", set.Dynamic)
	}
	for _, spec := range set.All() {
		if spec.Source != forwardSourceOpenSSHG {
			t.Fatalf("source: %+v", spec)
		}
	}
}

func TestParseForwardSpecLine(t *testing.T) {
	spec, err := ParseForwardSpecLine("  LocalForward 8888 127.0.0.1:8000  ")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != ForwardKindLocal || spec.BindPort != 8888 || spec.RemoteHost != "127.0.0.1" || spec.RemotePort != 8000 {
		t.Fatalf("got %+v", spec)
	}
	spec, err = ParseForwardSpecLine("RemoteForward 2222 localhost:22")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != ForwardKindRemote || spec.BindPort != 2222 || spec.LocalHost != "localhost" || spec.LocalPort != 22 {
		t.Fatalf("got %+v", spec)
	}
	spec, err = ParseForwardSpecLine("DynamicForward 127.0.0.1:1080")
	if err != nil {
		t.Fatal(err)
	}
	if spec.Kind != ForwardKindDynamic || spec.BindHost != "127.0.0.1" || spec.BindPort != 1080 {
		t.Fatalf("got %+v", spec)
	}
}

func TestPickForward(t *testing.T) {
	specs := []ForwardSpec{
		{Kind: ForwardKindLocal, BindPort: 8080, RemotePort: 5432},
		{Kind: ForwardKindLocal, BindPort: 9090, RemotePort: 3306},
	}
	got, err := PickForward(specs, "5432")
	if err != nil || got.BindPort != 8080 {
		t.Fatalf("remote port match: %+v err=%v", got, err)
	}
	got, err = PickForward(specs, "9090")
	if err != nil || got.RemotePort != 3306 {
		t.Fatalf("bind port match: %+v err=%v", got, err)
	}
	_, err = PickForward(specs, "1234")
	if err == nil {
		t.Fatal("expected miss")
	}
}

func TestForwardsFromSSHConfigFallback(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	content := "Host myhost\n  LocalForward 7777 127.0.0.1:6379\n  RemoteForward 2222 localhost:22\n"
	if err := os.WriteFile(cfg, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)
	// ssh config lives at $HOME/.ssh/config
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	set, err := forwardsFromSSHConfigFallback("myhost")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Local) != 1 || set.Local[0].BindPort != 7777 || set.Local[0].RemotePort != 6379 {
		t.Fatalf("local: %+v", set.Local)
	}
	if len(set.Remote) != 1 || !set.Remote[0].FallbackWarn || set.Remote[0].Source != forwardSourceFallbackParser {
		t.Fatalf("remote: %+v", set.Remote)
	}
}

func TestCollectSSHConfigIncludes_rejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret")
	if err := os.WriteFile(secret, []byte("Host x\n  LocalForward 1 127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(sshDir, "config")
	if err := os.WriteFile(main, []byte("Include ../secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	_, err := collectSSHConfigIncludes(sshDir, main, 0)
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("expected outside error, got %v", err)
	}
}

func TestCollectSSHConfigIncludes_nestedUnderSSH(t *testing.T) {
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	confDir := filepath.Join(sshDir, "conf.d")
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(confDir, "extra"), []byte("Host nested\n  LocalForward 5555 127.0.0.1:9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "config"), []byte("Include conf.d/extra\nHost main\n  LocalForward 1111 127.0.0.1:1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", dir)

	files, err := collectSSHConfigIncludes(sshDir, filepath.Join(sshDir, "config"), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("files=%v", files)
	}

	set, err := forwardsFromSSHConfigFallback("nested")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Local) != 1 || set.Local[0].BindPort != 5555 {
		t.Fatalf("expected nested forward, got %+v", set.Local)
	}
}

func TestHostPatternMatches(t *testing.T) {
	if !hostPatternMatches("*.example.com", "db.example.com") {
		t.Fatal("suffix wildcard")
	}
	if hostPatternMatches("other", "db.example.com") {
		t.Fatal("should not match")
	}
}

func TestForwardsCacheKey_stable(t *testing.T) {
	k1 := forwardsCacheKey("host", map[string]string{"B": "2", "A": "1"})
	k2 := forwardsCacheKey("host", map[string]string{"A": "1", "B": "2"})
	if k1 != k2 {
		t.Fatalf("keys differ: %q vs %q", k1, k2)
	}
	if strings.Contains(forwardsCacheKey("host", nil), "\x00") {
		t.Fatal("empty env should use dest only")
	}
}
