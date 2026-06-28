// Package commandrisk analyzes recipe commands for dangerous patterns. It parses
// a command to an AST and emits deterministic risk signals — it never executes
// anything and performs no network I/O. Shell commands are parsed with
// mvdan.cc/sh; Python steps (interpreter "python3") are parsed with gpython, and
// shell strings passed to os.system / subprocess.* recurse into the shell
// detectors. The signals feed an OPA policy decision and user-facing risk
// review; an LLM may later explain them but is never authoritative.
package commandrisk

// Severity ranks a risk signal. Critical signals are hard-denied by the engine
// regardless of policy.
type Severity string

// Severity levels, ascending. SeverityCritical triggers a built-in hard deny.
const (
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

// rank orders severities for MaxSeverity comparison.
func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 4
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	default:
		return 0
	}
}

// RiskSignal is one detected risk in a command.
type RiskSignal struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Command  string   `json:"command,omitempty"`
	Args     []string `json:"args,omitempty"`
	Reason   string   `json:"reason"`
}

// Detected summarizes the parsed command surface, for policy input and display.
type Detected struct {
	Commands []string `json:"commands"`
	Flags    []string `json:"flags"`
	Paths    []string `json:"paths"`
}

// Analysis is the deterministic result for one command string.
type Analysis struct {
	Signals     []RiskSignal `json:"signals"`
	Detected    Detected     `json:"detected"`
	MaxSeverity Severity     `json:"max_severity,omitempty"`
	Critical    bool         `json:"critical"`
	// ParseError is set when the command could not be parsed; this also yields a
	// medium UNPARSEABLE_COMMAND signal rather than a hard failure.
	ParseError string `json:"parse_error,omitempty"`
	// Interpreter is the step's declared interpreter (e.g. "python3", "bash"), or
	// empty for the default shell. It selects the parser and is passed to policy.
	Interpreter string `json:"interpreter,omitempty"`

	seenCommands map[string]struct{}
	seenFlags    map[string]struct{}
	seenPaths    map[string]struct{}
}

// add records a signal and updates MaxSeverity / Critical.
func (a *Analysis) add(s RiskSignal) {
	a.Signals = append(a.Signals, s)
	if s.Severity.rank() > a.MaxSeverity.rank() {
		a.MaxSeverity = s.Severity
	}
	if s.Severity == SeverityCritical {
		a.Critical = true
	}
}

// FirstCritical returns the first critical signal, or nil when none.
func (a *Analysis) FirstCritical() *RiskSignal {
	for i := range a.Signals {
		if a.Signals[i].Severity == SeverityCritical {
			return &a.Signals[i]
		}
	}
	return nil
}
