package commandrisk

import (
	"strings"

	"github.com/go-python/gpython/ast"
	"github.com/go-python/gpython/parser"
	"github.com/go-python/gpython/py"
)

// blockDevicePrefixes are device paths whose overwrite destroys a disk.
var blockDevicePrefixes = []string{"/dev/sd", "/dev/nvme", "/dev/vd"}

// analyzePython parses Python source with gpython (no execution) and emits risk
// signals from a deterministic AST walk. Shell strings handed to os.system /
// subprocess.* recurse into the shell analyzer so a python wrapper around
// "rm -rf /" still yields the shell critical. A parse failure is a medium
// signal, mirroring the shell analyzer's tolerance for unparseable input.
func analyzePython(src string) Analysis {
	var a Analysis
	if strings.TrimSpace(src) == "" {
		return a
	}

	mod, err := parser.ParseString(src, py.ExecMode)
	if err != nil {
		a.ParseError = err.Error()
		a.add(RiskSignal{ID: "PYTHON_PARSE_ERROR", Severity: SeverityMedium, Reason: "python source could not be parsed"})
		return a
	}

	ast.Walk(mod, func(node ast.Ast) bool {
		if call, ok := node.(*ast.Call); ok {
			a.inspectPyCall(call)
		}
		return true
	})
	return a
}

// inspectPyCall classifies one Python function call by its dotted name.
func (a *Analysis) inspectPyCall(c *ast.Call) {
	name := callName(c.Func)
	if name == "" {
		return
	}
	a.Detected.Commands = appendUnique(a.Detected.Commands, name)

	switch name {
	case "os.system", "os.popen",
		"subprocess.run", "subprocess.call", "subprocess.Popen",
		"subprocess.check_output", "subprocess.check_call":
		a.detectPyShellExec(name, c)
	case "shutil.rmtree":
		a.detectPyRmtree(c)
	case "os.remove", "os.unlink", "os.rmdir":
		a.detectPyDelete(name, c)
	case "eval", "exec", "compile":
		a.add(RiskSignal{ID: "PYTHON_DYNAMIC_EXEC", Severity: SeverityMedium, Command: name, Reason: "dynamic code execution"})
	case "open":
		a.detectPyOpenBlockDevice(c)
	}
}

// detectPyShellExec recurses a literal shell argument into the shell analyzer,
// merging its signals; a non-literal (runtime-built) command is a medium signal.
func (a *Analysis) detectPyShellExec(name string, c *ast.Call) {
	if len(c.Args) == 0 {
		return
	}
	shell, ok := pyStrLit(c.Args[0])
	if !ok {
		a.add(RiskSignal{ID: "PYTHON_DYNAMIC_SHELL_EXEC", Severity: SeverityMedium, Command: name, Reason: "shell command built at runtime"})
		return
	}
	sub := Analyze(shell)
	for _, s := range sub.Signals {
		a.add(s)
	}
	a.Detected.Commands = appendUniqueAll(a.Detected.Commands, sub.Detected.Commands)
	a.Detected.Flags = appendUniqueAll(a.Detected.Flags, sub.Detected.Flags)
	a.Detected.Paths = appendUniqueAll(a.Detected.Paths, sub.Detected.Paths)
}

// detectPyRmtree flags shutil.rmtree: critical on a literal system path, high
// on any other literal path, high when the target is built at runtime.
func (a *Analysis) detectPyRmtree(c *ast.Call) {
	if len(c.Args) == 0 {
		return
	}
	path, ok := pyStrLit(c.Args[0])
	if !ok {
		a.add(RiskSignal{ID: "PYTHON_RMTREE_DYNAMIC", Severity: SeverityHigh, Command: "shutil.rmtree", Reason: "recursive delete of a runtime path"})
		return
	}
	if isSystemPath(path) {
		a.add(RiskSignal{ID: "PYTHON_RMTREE_SYSTEM_PATH", Severity: SeverityCritical, Command: "shutil.rmtree", Args: []string{path}, Reason: "recursive delete of a system/root path"})
		return
	}
	a.add(RiskSignal{ID: "PYTHON_RMTREE", Severity: SeverityHigh, Command: "shutil.rmtree", Args: []string{path}, Reason: "recursive delete"})
}

// detectPyDelete flags os.remove/unlink/rmdir: critical on a literal system path.
func (a *Analysis) detectPyDelete(name string, c *ast.Call) {
	if len(c.Args) == 0 {
		return
	}
	if path, ok := pyStrLit(c.Args[0]); ok && isSystemPath(path) {
		a.add(RiskSignal{ID: "PYTHON_DELETE_SYSTEM_PATH", Severity: SeverityCritical, Command: name, Args: []string{path}, Reason: "delete of a system/root path"})
		return
	}
	a.add(RiskSignal{ID: "PYTHON_FILE_DELETE", Severity: SeverityMedium, Command: name, Reason: "file deletion"})
}

// detectPyOpenBlockDevice flags open("/dev/sdX", "w…") — writing a raw device.
func (a *Analysis) detectPyOpenBlockDevice(c *ast.Call) {
	if len(c.Args) < 2 {
		return
	}
	path, ok := pyStrLit(c.Args[0])
	if !ok || !isBlockDevicePath(path) {
		return
	}
	mode, ok := pyStrLit(c.Args[1])
	if !ok || !strings.ContainsAny(mode, "wa") {
		return
	}
	a.add(RiskSignal{ID: "DD_WRITE_BLOCK_DEVICE", Severity: SeverityCritical, Command: "open", Args: []string{path}, Reason: "writing to a block device"})
}

// callName renders a call target to its dotted name: os.system, subprocess.run,
// eval. Returns "" for forms that are not a plain name/attribute chain.
func callName(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.Name:
		return string(n.Id)
	case *ast.Attribute:
		base := callName(n.Value)
		if base == "" {
			return ""
		}
		return base + "." + string(n.Attr)
	default:
		return ""
	}
}

// pyStrLit extracts a literal string argument: a string literal directly, or a
// list/tuple of string literals joined by spaces (subprocess(["rm","-rf","/"])).
func pyStrLit(e ast.Expr) (string, bool) {
	switch n := e.(type) {
	case *ast.Str:
		return string(n.S), true
	case *ast.List:
		return joinStrLits(n.Elts)
	case *ast.Tuple:
		return joinStrLits(n.Elts)
	default:
		return "", false
	}
}

// joinStrLits joins a slice of expressions when every element is a string
// literal, returning false otherwise.
func joinStrLits(elts []ast.Expr) (string, bool) {
	parts := make([]string, 0, len(elts))
	for _, e := range elts {
		s, ok := e.(*ast.Str)
		if !ok {
			return "", false
		}
		parts = append(parts, string(s.S))
	}
	return strings.Join(parts, " "), true
}

// isBlockDevicePath reports whether a path targets a raw block device.
func isBlockDevicePath(path string) bool {
	for _, p := range blockDevicePrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}
