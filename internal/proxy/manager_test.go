package proxy

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
)

func TestManager_StartListStop(t *testing.T) {
	os.Setenv("XDG_STATE_HOME", t.TempDir())
	config.ResolveStateDir() // Trigger load

	mgr := NewManager(nil)

	app := apps.AppConfig{
		Name:      "test-http",
		Type:      apps.AppTypeHTTP,
		Target:    "localhost",
		Upstream:  "example.com:80",
		LocalPort: 28080,
	}

	dialer := DirectDialer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sess, err := mgr.Start(ctx, app, dialer)
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
	os.Setenv("XDG_STATE_HOME", t.TempDir())

	mgr := NewManager(nil)
	app := apps.AppConfig{
		Name:      "test-tcp",
		Type:      apps.AppTypeTCP,
		Target:    "localhost",
		Upstream:  "example.com:80",
		LocalPort: 25432,
		TTL:       time.Millisecond * 10,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := mgr.Start(ctx, app, DirectDialer{})
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
