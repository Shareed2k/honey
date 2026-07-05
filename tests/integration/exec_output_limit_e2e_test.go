//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestExecOutputLimit(t *testing.T) {
	sshH, sshP, keyFile := startSSH(t)
	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)
	reg := &testRegistry{Dialer: newTestDialer(sshH, sshP, keyFile)}

	longOutput := strings.Repeat("A", 100)
	cmd := "echo " + longOutput

	t.Run("Unlimited Output", func(t *testing.T) {
		res := runOne(t, context.Background(), reg, rec, cmd, engine.BatchOptions{MaxOutputBytes: -1}, 10*time.Second)

		if !res.Success {
			t.Fatalf("expected success, got failure: %+v", res)
		}
		if strings.Contains(res.Output, "truncated") {
			t.Fatalf("output should not be truncated, got %q", res.Output)
		}
		if !strings.Contains(res.Output, longOutput) {
			t.Fatalf("output should contain full string, got %q", res.Output)
		}
	})

	t.Run("Truncated Output", func(t *testing.T) {
		res := runOne(t, context.Background(), reg, rec, cmd, engine.BatchOptions{MaxOutputBytes: 10}, 10*time.Second)

		if !res.Success {
			t.Fatalf("expected success, got failure: %+v", res)
		}
		if !strings.Contains(res.Output, "truncated") {
			t.Fatalf("output should be truncated, got %q", res.Output)
		}
		if !strings.HasPrefix(res.Output, "AAAAAAAAAA\n…(truncated)") {
			t.Fatalf("output does not match expected truncated string, got %q", res.Output)
		}
	})

	t.Run("Default Output Limit", func(t *testing.T) {
		cmdHuge := "echo " + strings.Repeat("B", 7000)
		res := runOne(t, context.Background(), reg, rec, cmdHuge, engine.BatchOptions{MaxOutputBytes: 0}, 10*time.Second)

		if !res.Success {
			t.Fatalf("expected success, got failure: %+v", res)
		}
		if !strings.Contains(res.Output, "truncated") {
			t.Fatalf("output should be truncated, got %q", res.Output)
		}
		if len(res.Output) > 6050 { // 6000 + length of truncation marker
			t.Fatalf("output is too long, expected ~6000 bytes, got %d", len(res.Output))
		}
	})
}
