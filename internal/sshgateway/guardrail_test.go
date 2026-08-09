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
)

// decisionRec captures one onDecision callback for assertions.
type decisionRec struct {
	command string
	reason  string
	denied  bool
}

// sentinelDecide denies any command containing sentinel; otherwise allows. It
// keeps tests deterministic and independent of the commandrisk ruleset.
func sentinelDecide(sentinel, reason string) func(context.Context, string) (string, bool) {
	return func(_ context.Context, cmd string) (string, bool) {
		if strings.Contains(cmd, sentinel) {
			return reason, true
		}
		return "", false
	}
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

func TestGuardReader_modes(t *testing.T) {
	t.Parallel()
	const sentinel = "rm -rf /"
	const denyReason = "test policy: dangerous"

	tests := []struct {
		name         string
		mode         guardMode
		input        string
		wantContains []byte // bytes the output MUST contain
		wantAbsent   []byte // bytes the output must NOT contain (nil to skip)
		wantNotify   string // substring the notify sink must contain ("" = empty)
		wantDecided  bool   // whether onDecision fired
		wantDenied   bool   // expected denied flag when decided
		decideCalled bool   // whether decide may be called at all
		wantCommand  string // command decide/onDecision should have seen
	}{
		{
			name:         "off is transparent, decide never called",
			mode:         guardOff,
			input:        sentinel + "\n",
			wantContains: []byte(sentinel + "\n"),
			wantNotify:   "",
			wantDecided:  false,
			decideCalled: false,
		},
		{
			name:         "audit denied still forwards Enter",
			mode:         guardAudit,
			input:        sentinel + "\n",
			wantContains: []byte("\n"),
			wantNotify:   "",
			wantDecided:  true,
			wantDenied:   true,
			decideCalled: true,
			wantCommand:  sentinel,
		},
		{
			name:         "enforce denied injects Ctrl-U and blocks Enter",
			mode:         guardEnforce,
			input:        sentinel + "\n",
			wantContains: []byte{ctrlU},
			wantAbsent:   []byte("\n"),
			wantNotify:   "blocked by policy",
			wantDecided:  true,
			wantDenied:   true,
			decideCalled: true,
			wantCommand:  sentinel,
		},
		{
			name:         "enforce allowed forwards Enter",
			mode:         guardEnforce,
			input:        "ls -la\n",
			wantContains: []byte("\n"),
			wantNotify:   "",
			wantDecided:  true,
			wantDenied:   false,
			decideCalled: true,
			wantCommand:  "ls -la",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var (
				notify   bytes.Buffer
				recorded []decisionRec
				decided  []string
			)
			decideBase := sentinelDecide(sentinel, denyReason)
			decide := func(ctx context.Context, cmd string) (string, bool) {
				if !tt.decideCalled {
					t.Fatalf("decide must not be called in %s mode, got %q", tt.mode, cmd)
				}
				decided = append(decided, cmd)
				return decideBase(ctx, cmd)
			}
			onDecision := func(cmd, reason string, denied bool) {
				recorded = append(recorded, decisionRec{cmd, reason, denied})
			}

			r := newGuardReader(context.Background(), strings.NewReader(tt.input), &notify, tt.mode, decide, onDecision)
			out := drainChunks(t, r, 0)

			for _, b := range tt.wantContains {
				if !bytes.Contains(out, []byte{b}) {
					t.Fatalf("output %q missing byte %#x", out, b)
				}
			}
			if tt.wantAbsent != nil {
				for _, b := range tt.wantAbsent {
					if bytes.Contains(out, []byte{b}) {
						t.Fatalf("output %q must not contain byte %#x", out, b)
					}
				}
			}
			if tt.mode == guardOff && !bytes.Equal(out, []byte(tt.input)) {
				t.Fatalf("off mode output %q != input %q", out, tt.input)
			}
			if got := notify.String(); tt.wantNotify == "" {
				if got != "" {
					t.Fatalf("notify = %q, want empty", got)
				}
			} else if !strings.Contains(got, tt.wantNotify) {
				t.Fatalf("notify = %q, want contains %q", got, tt.wantNotify)
			}
			if tt.wantDecided {
				if len(recorded) != 1 {
					t.Fatalf("onDecision fired %d times, want 1: %+v", len(recorded), recorded)
				}
				if recorded[0].denied != tt.wantDenied {
					t.Fatalf("denied = %v, want %v", recorded[0].denied, tt.wantDenied)
				}
				if recorded[0].command != tt.wantCommand {
					t.Fatalf("command = %q, want %q", recorded[0].command, tt.wantCommand)
				}
			} else if len(recorded) != 0 {
				t.Fatalf("onDecision fired but should not have: %+v", recorded)
			}
			if tt.decideCalled && len(decided) == 0 {
				t.Fatal("decide was never called")
			}
		})
	}
}

func TestGuardReader_backspaceReconstruction(t *testing.T) {
	t.Parallel()
	var seen []string
	decide := func(_ context.Context, cmd string) (string, bool) {
		seen = append(seen, cmd)
		return "", false
	}
	onDecision := func(string, string, bool) {}

	// "rm -rf /x" then DEL erases the 'x', so the reconstructed command is
	// "rm -rf /".
	r := newGuardReader(context.Background(), strings.NewReader("rm -rf /x\x7f\n"), io.Discard, guardEnforce, decide, onDecision)
	_ = drainChunks(t, r, 0)

	if len(seen) != 1 {
		t.Fatalf("decide calls = %v, want exactly one", seen)
	}
	if seen[0] != "rm -rf /" {
		t.Fatalf("reconstructed command = %q, want %q", seen[0], "rm -rf /")
	}
}

func TestGuardReader_tinyChunksMatchBigRead(t *testing.T) {
	t.Parallel()
	const sentinel = "danger"
	const input = "echo ok\n" + sentinel + " now\n" + "ls\n"

	run := func(chunk int) ([]byte, []decisionRec, string) {
		var (
			notify   bytes.Buffer
			recorded []decisionRec
		)
		decide := sentinelDecide(sentinel, "nope")
		onDecision := func(cmd, reason string, denied bool) {
			recorded = append(recorded, decisionRec{cmd, reason, denied})
		}
		r := newGuardReader(context.Background(), strings.NewReader(input), &notify, guardEnforce, decide, onDecision)
		return drainChunks(t, r, chunk), recorded, notify.String()
	}

	big, bigRec, bigNotify := run(0)
	tiny, tinyRec, tinyNotify := run(1)

	if !bytes.Equal(big, tiny) {
		t.Fatalf("tiny-chunk output %q != big-read output %q", tiny, big)
	}
	if bigNotify != tinyNotify {
		t.Fatalf("tiny-chunk notify %q != big-read notify %q", tinyNotify, bigNotify)
	}
	if len(bigRec) != len(tinyRec) {
		t.Fatalf("decision count differs: big=%d tiny=%d", len(bigRec), len(tinyRec))
	}
	for i := range bigRec {
		if bigRec[i] != tinyRec[i] {
			t.Fatalf("decision %d differs: big=%+v tiny=%+v", i, bigRec[i], tinyRec[i])
		}
	}
	// Sanity: the denied middle command dropped its \n and gained a Ctrl-U, so
	// the output has exactly two newlines (the two allowed commands).
	if got := bytes.Count(big, []byte{'\n'}); got != 2 {
		t.Fatalf("newline count = %d, want 2 (denied command blocked)", got)
	}
	if !bytes.Contains(big, []byte{ctrlU}) {
		t.Fatalf("output %q missing injected Ctrl-U", big)
	}
}

// TestGuardReader_realGateEnforceBlocks wires the guard to the REAL risk+policy
// gate (cmdgate.AssessTargets + an OPA enforcer denying command_exec) to prove
// the whole enforce path — reconstruct, assess, inject Ctrl-U, notify — holds
// with production primitives, not just a stub decide.
func TestGuardReader_realGateEnforceBlocks(t *testing.T) {
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
			[]cmdgate.TargetInput{{Name: rec.Name, PolicyInput: commandPolicyInput("alice", rec, cmd), Attrs: recordAttrs(rec)}}, false)
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
	r := newGuardReader(context.Background(), strings.NewReader("whoami\n"), &notify, guardEnforce, decide, onDecision)
	out := drainChunks(t, r, 0)

	if bytes.Contains(out, []byte{'\n'}) {
		t.Fatalf("denied command forwarded its Enter: %q", out)
	}
	if !bytes.Contains(out, []byte{ctrlU}) {
		t.Fatalf("output %q missing injected Ctrl-U", out)
	}
	if !strings.Contains(notify.String(), "command_exec blocked by test policy") {
		t.Fatalf("notify = %q, want the OPA deny reason", notify.String())
	}
	if len(recorded) != 1 || !recorded[0].denied {
		t.Fatalf("recorded = %+v, want one denied decision", recorded)
	}
}
