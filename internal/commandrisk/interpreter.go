package commandrisk

import (
	"path"
	"strings"
)

// AnalyzeStep analyzes a recipe step's command with the parser matching its
// interpreter: the default shell (empty) or a shell interpreter uses the
// mvdan/sh analyzer; python uses the gpython analyzer; any other interpreter is
// left unparsed (no bogus signals) and deferred to policy. The interpreter is
// recorded on the result so it can be passed to OPA and shown in reviews.
func AnalyzeStep(command, interpreter string) Analysis {
	interp := strings.TrimSpace(interpreter)
	var a Analysis
	switch {
	case interp == "" || isShellInterpreter(interp):
		a = Analyze(command)
	case isPythonInterpreter(interp):
		a = analyzePython(command)
	default:
		// Unknown interpreter: no shell parse, rely on the OPA decision.
	}
	a.Interpreter = interp
	return a
}

// interpreterBase returns the bare program name of an interpreter spec, dropping
// any directory and trailing arguments (e.g. "/usr/bin/python3 -u" → "python3").
func interpreterBase(interpreter string) string {
	fields := strings.Fields(interpreter)
	if len(fields) == 0 {
		return ""
	}
	return path.Base(fields[0])
}

// isShellInterpreter reports whether the interpreter is a POSIX-style shell.
func isShellInterpreter(interpreter string) bool {
	return isShell(interpreterBase(interpreter))
}

// isPythonInterpreter reports whether the interpreter is a CPython binary
// (python, python2, python3, python3.12, …).
func isPythonInterpreter(interpreter string) bool {
	base := interpreterBase(interpreter)
	if !strings.HasPrefix(base, "python") {
		return false
	}
	rest := strings.TrimPrefix(base, "python")
	for _, r := range rest {
		if (r < '0' || r > '9') && r != '.' {
			return false
		}
	}
	return true
}
