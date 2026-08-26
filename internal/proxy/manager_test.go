package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
)

func TestManager_StartListStop(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	config.ResolveStateDir() // Trigger load

	mgr := NewManager(nil)

	app := apps.AppConfig{
		Name:     "test-http",
		Type:     apps.AppTypeHTTP,
		Target:   "localhost",
		Upstream: "example.com:80",
		// LocalPort 0 = dynamic web-proxy mode: StartHTTPProxy binds no local
		// listener (see http.go "if app.LocalPort > 0"), so this test exercises the
		// Manager session lifecycle deterministically. A hardcoded port raced the
		// 100ms bind heuristic and collided with whatever already held it on a CI
		// runner — the source of the TestManager_StartListStop flake.
		LocalPort: 0,
	}

	dialer := DirectDialer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer mgr.Wait() // runs after cancel() below (LIFO): no state write outlives the test
	defer cancel()

	sess, err := mgr.Start(ctx, app, dialer, nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(sessions) != 1 {
		t.Fatalf("Expected 1 session, got %d", len(sessions))
	}
	if sessions[0].ID != sess.ID {
		t.Fatalf("Expected session ID %s, got %s", sess.ID, sessions[0].ID)
	}

	if err := mgr.Stop(sess.ID); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	sessions, err = mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("Expected 0 sessions after stop, got %d", len(sessions))
	}
}

func TestManager_Expired(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	mgr := NewManager(nil)
	app := apps.AppConfig{
		Name:     "test-tcp",
		Type:     apps.AppTypeTCP,
		Target:   "localhost",
		Upstream: "example.com:80",
		// LocalPort 0 lets StartTCPProxy bind an OS-assigned ephemeral port
		// (net.Listen "127.0.0.1:0") instead of a hardcoded one that could be in
		// use on a CI runner — same fixed-port flake class as StartListStop.
		LocalPort: 0,
		TTL:       time.Millisecond * 10,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer mgr.Wait() // runs after cancel() below (LIFO): no state write outlives the test
	defer cancel()

	_, err := mgr.Start(ctx, app, DirectDialer{}, nil)
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	sessions, err := mgr.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("Expected expired session to be removed, got %d", len(sessions))
	}
}
