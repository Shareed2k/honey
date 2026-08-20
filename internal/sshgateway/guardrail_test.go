package sshgateway

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cmdgate"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/termguard"
)

// decisionRec captures one onDecision callback for assertions.
type decisionRec struct {
	command string
	reason  string
	denied  bool
}

// drainChunks fully reads r using a buffer of size chunk, returning all output
// bytes. chunk == 0 means one big read via io.ReadAll.
func drainChunks(t *testing.T, r io.Reader, chunk int) []byte {
	t.Helper()
	if chunk <= 0 {
		out, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("read all: %v", err)
		}
		return out
	}
	var acc bytes.Buffer
	buf := make([]byte, chunk)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			acc.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read chunk: %v", err)
		}
	}
	return acc.Bytes()
}

// TestGuardWiring_realGateEnforceBlocks wires termguard.NewReader to the REAL
// risk+policy gate (cmdgate.AssessTargets + an OPA enforcer denying
// command_exec) via cmdgate's shared, exported policy-input helpers — the
// exact call shape runInteractive builds (session.go's decide closure) — to
// prove the whole enforce path (reconstruct, assess, inject Ctrl-U, notify)
// still holds with production primitives after the guard mechanism moved to
// internal/termguard and the policy-input builders moved to cmdgate.
func TestGuardWiring_realGateEnforceBlocks(t *testing.T) {
	t.Parallel()
	const src = `package honey
import rego.v1
default allow := true
default deny_reason := ""
allow := false if input.action == "command_exec"
deny_reason := "command_exec blocked by test policy" if input.action == "command_exec"
`
	enf, err := policy.NewFromSource(context.Background(), "deny.rego", src)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	rec := hosts.Record{Provider: "local", Name: "web1", PrimaryIP: "127.0.0.1"}

	decide := func(ctx context.Context, cmd string) (string, bool) {
		_, decisions, derr := cmdgate.AssessTargets(ctx, enf, nil, cmd, "sh",
			[]cmdgate.TargetInput{{Name: rec.Name, PolicyInput: cmdgate.CommandPolicyInput("alice", rec, cmd), Attrs: cmdgate.RecordAttrs(rec)}}, false)
		if derr != nil {
			return "policy error: " + derr.Error(), true
		}
		if len(decisions) > 0 && decisions[0].Denied {
			return decisions[0].Reason, true
		}
		return "", false
	}
	var recorded []decisionRec
	onDecision := func(cmd, reason string, denied bool) {
		recorded = append(recorded, decisionRec{cmd, reason, denied})
	}

	var notify bytes.Buffer
	r := termguard.NewReader(context.Background(), strings.NewReader("whoami\n"), &notify, termguard.ModeEnforce, decide, onDecision)
	out := drainChunks(t, r, 0)

	if bytes.Contains(out, []byte{'\n'}) {
		t.Fatalf("denied command forwarded its Enter: %q", out)
	}
	// 0x15 is Ctrl-U (kill-line), injected in place of the blocked Enter.
	if !bytes.Contains(out, []byte{0x15}) {
		t.Fatalf("output %q missing injected Ctrl-U", out)
	}
	if !strings.Contains(notify.String(), "command_exec blocked by test policy") {
		t.Fatalf("notify = %q, want the OPA deny reason", notify.String())
	}
	if len(recorded) != 1 || !recorded[0].denied {
		t.Fatalf("recorded = %+v, want one denied decision", recorded)
	}
}

// TestGuardModeVal_defaultsOff proves the gateway's own mode resolution
// (guardModeVal, wired to termguard.ParseMode) keeps its documented
// fail-safe default after the lift: an empty or unrecognized config value
// never accidentally enables interception.
func TestGuardModeVal_defaultsOff(t *testing.T) {
	t.Parallel()
	s := &Server{opts: Options{GuardMode: "bogus"}}
	if got := s.guardModeVal(); got != termguard.ModeOff {
		t.Fatalf("guardModeVal() = %q, want %q", got, termguard.ModeOff)
	}
	s.opts.GuardMode = "Enforce"
	if got := s.guardModeVal(); got != termguard.ModeEnforce {
		t.Fatalf("guardModeVal() = %q, want %q", got, termguard.ModeEnforce)
	}
}
