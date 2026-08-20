package termguard

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
)

// decisionRec captures one onDecision callback for assertions.
type decisionRec struct {
	command string
	reason  string
	denied  bool
}

// sentinelDecide denies any command containing sentinel; otherwise allows. It
// keeps tests deterministic and independent of any real risk/policy engine.
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
		mode         Mode
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
			mode:         ModeOff,
			input:        sentinel + "\n",
			wantContains: []byte(sentinel + "\n"),
			wantNotify:   "",
			wantDecided:  false,
			decideCalled: false,
		},
		{
			name:         "audit denied still forwards Enter",
			mode:         ModeAudit,
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
			mode:         ModeEnforce,
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
			mode:         ModeEnforce,
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

			r := NewReader(context.Background(), strings.NewReader(tt.input), &notify, tt.mode, decide, onDecision)
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
			if tt.mode == ModeOff && !bytes.Equal(out, []byte(tt.input)) {
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
	r := NewReader(context.Background(), strings.NewReader("rm -rf /x\x7f\n"), io.Discard, ModeEnforce, decide, onDecision)
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
		r := NewReader(context.Background(), strings.NewReader(input), &notify, ModeEnforce, decide, onDecision)
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

// TestReader_lineCap proves the reconstructed line handed to decide is capped
// at lineMax bytes (a client that never sends a newline cannot grow the
// buffer without limit), while every byte is still forwarded to the target
// untouched — the cap only bounds the RECONSTRUCTION, never the output.
func TestGuardReader_lineCap(t *testing.T) {
	t.Parallel()
	overflow := 100
	input := strings.Repeat("a", lineMax+overflow) + "\n"

	var seen string
	decide := func(_ context.Context, cmd string) (string, bool) {
		seen = cmd
		return "", false
	}
	onDecision := func(string, string, bool) {}

	r := NewReader(context.Background(), strings.NewReader(input), io.Discard, ModeEnforce, decide, onDecision)
	out := drainChunks(t, r, 0)

	if len(seen) != lineMax {
		t.Fatalf("reconstructed line length = %d, want %d (capped)", len(seen), lineMax)
	}
	if len(out) != len(input) {
		t.Fatalf("output length = %d, want %d (every byte still forwarded past the cap)", len(out), len(input))
	}
}

// TestReader_SnapshotRestore proves Restore rolls the reconstructed line
// back to exactly what Snapshot captured — not a blind clear — undoing
// whatever process() did since, including a completed line's Enter (which
// unconditionally clears the line, whether forwarded or replaced with a
// Ctrl-U). A caller that drives Read from discrete, already-delivered
// messages rather than a continuous stream (see internal/webserver's guest
// relay guard) needs this when bytes it already fed in ultimately never
// reached the target: the target's own line buffer didn't change either, so
// rolling back to the pre-frame snapshot is what keeps guard state in sync
// with it — a blind clear would instead forget a still-live line the target
// actually has pending.
func TestReader_SnapshotRestore(t *testing.T) {
	t.Parallel()
	var seen []string
	decide := func(_ context.Context, cmd string) (string, bool) {
		seen = append(seen, cmd)
		return "denied by test", true
	}
	onDecision := func(string, string, bool) {}

	r := NewReader(context.Background(), strings.NewReader("unused"), io.Discard, ModeEnforce, decide, onDecision)
	rr, ok := r.(*reader)
	if !ok {
		t.Fatal("NewReader must return *reader for a non-off mode")
	}

	// Establish live, already-relayed content, mirroring what a target
	// really has pending — the snapshot below must capture exactly this.
	rr.process([]byte("rm -rf "))
	snap := rr.Snapshot()

	// This "frame" completes a DENIED line: process() replaces the Enter
	// with a Ctrl-U and unconditionally clears the line, before the caller
	// even knows whether the relay carrying these bytes will succeed.
	chunk := []byte("poison\n")
	rr.process(chunk)
	if len(rr.line) != 0 {
		t.Fatalf("line = %q, want empty right after a completed Enter (process's own unconditional clear)", rr.line)
	}
	if len(seen) != 1 || seen[0] != "rm -rf poison" {
		t.Fatalf("seen = %v, want exactly one decide on the spliced line before rollback", seen)
	}

	// The frame carrying "poison\n" (now "poison"+Ctrl-U) never reached the
	// target — restore to the pre-frame snapshot, not an empty line.
	rr.Restore(snap)
	if string(rr.line) != "rm -rf " {
		t.Fatalf("line = %q, want %q restored from the snapshot", rr.line, "rm -rf ")
	}

	// A later bare Enter must decide on the RESTORED line, not an empty one.
	rr.process([]byte("/\n"))
	if len(seen) != 2 || seen[1] != "rm -rf /" {
		t.Fatalf("seen = %v, want the second decide on the restored+completed line \"rm -rf /\"", seen)
	}
}

// TestReader_realGateWiring wires the reader to a decide function that mirrors
// the shape both consumers (sshgateway, webserver) build in production —
// reconstruct, assess, inject Ctrl-U, notify — proving the whole enforce path
// holds end to end, not just with a trivial stub decide.
func TestGuardReader_realGateWiring(t *testing.T) {
	t.Parallel()
	decide := func(_ context.Context, cmd string) (string, bool) {
		if strings.Contains(cmd, "whoami") {
			return "command_exec blocked by test policy", true
		}
		return "", false
	}
	var recorded []decisionRec
	onDecision := func(cmd, reason string, denied bool) {
		recorded = append(recorded, decisionRec{cmd, reason, denied})
	}

	var notify bytes.Buffer
	r := NewReader(context.Background(), strings.NewReader("whoami\n"), &notify, ModeEnforce, decide, onDecision)
	out := drainChunks(t, r, 0)

	if bytes.Contains(out, []byte{'\n'}) {
		t.Fatalf("denied command forwarded its Enter: %q", out)
	}
	if !bytes.Contains(out, []byte{ctrlU}) {
		t.Fatalf("output %q missing injected Ctrl-U", out)
	}
	if !strings.Contains(notify.String(), "command_exec blocked by test policy") {
		t.Fatalf("notify = %q, want the policy deny reason", notify.String())
	}
	if len(recorded) != 1 || !recorded[0].denied {
		t.Fatalf("recorded = %+v, want one denied decision", recorded)
	}
}
