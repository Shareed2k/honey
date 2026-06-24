package commandrisk

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// systemPaths are roots whose recursive deletion / permission change is critical.
var systemPaths = []string{"/", "/*", "/bin", "/boot", "/dev", "/etc", "/lib", "/sbin", "/usr", "/var", "/home", "/root"}

// Analyze parses a shell command and returns deterministic risk signals. A parse
// failure is reported as a medium signal, not an error — the gate treats
// unparseable commands as suspicious but not hard-denied.
func Analyze(command string) Analysis {
	var a Analysis
	if strings.TrimSpace(command) == "" {
		return a
	}

	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		a.ParseError = err.Error()
		a.add(RiskSignal{ID: "UNPARSEABLE_COMMAND", Severity: SeverityMedium, Reason: "command could not be parsed as shell"})
		return a
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			a.inspectCall(n)
		case *syntax.BinaryCmd:
			if n.Op == syntax.Pipe || n.Op == syntax.PipeAll {
				a.inspectPipe(n)
			}
		case *syntax.CmdSubst:
			a.inspectCmdSubst(n)
		case *syntax.Redirect:
			a.inspectRedirect(n)
		}
		return true
	})
	return a
}

// inspectCall classifies one simple command and runs per-command detectors.
func (a *Analysis) inspectCall(c *syntax.CallExpr) {
	if len(c.Args) == 0 {
		return
	}
	name := c.Args[0].Lit()
	if name == "" {
		return
	}
	flags, paths, rawArgs := splitArgs(c.Args[1:])
	short := shortFlagChars(flags) // e.g. ["-rf"] → set{r,f}

	a.Detected.Commands = appendUnique(a.Detected.Commands, name)
	a.Detected.Flags = appendUniqueAll(a.Detected.Flags, flags)
	a.Detected.Paths = appendUniqueAll(a.Detected.Paths, paths)

	switch name {
	case "rm":
		a.detectRm(short, flags, paths, c.Args[1:])
	case "dd":
		a.detectDD(rawArgs)
	case "mkfs", "mkfs.ext4", "mkfs.xfs", "mkfs.btrfs", "mkfs.vfat":
		a.add(RiskSignal{ID: "MKFS_FILESYSTEM", Severity: SeverityCritical, Command: name, Reason: "filesystem creation destroys existing data"})
	case "chmod":
		a.detectRecursivePerm(name, "CHMOD_RECURSIVE_SYSTEM_PATH", short, flags, paths)
	case "chown":
		a.detectRecursivePerm(name, "CHOWN_RECURSIVE_SYSTEM_PATH", short, flags, paths)
	case "sudo", "doas":
		a.add(RiskSignal{ID: "SUDO_PRIVILEGE_ESCALATION", Severity: SeverityHigh, Command: name, Reason: "privilege escalation"})
	case "systemctl", "service":
		a.detectServiceControl(name, rawArgs)
	case "kill", "pkill", "killall":
		a.add(RiskSignal{ID: "KILL_SIGNAL", Severity: SeverityMedium, Command: name, Args: rawArgs, Reason: "process termination"})
	case "kubectl":
		a.detectKubectl(rawArgs)
	case "helm":
		a.detectHelm(rawArgs)
	case "docker":
		a.detectDocker(rawArgs, flags)
	case "aws":
		a.detectAWS(rawArgs)
	case "gcloud":
		a.detectGcloud(rawArgs)
	case "apt", "apt-get", "yum", "dnf", "apk":
		if containsAny(rawArgs, "remove", "purge", "uninstall") {
			a.add(RiskSignal{ID: "PACKAGE_REMOVE", Severity: SeverityMedium, Command: name, Reason: "package removal"})
		}
	}

	a.detectForkBomb(name)
}

func (a *Analysis) detectRm(short map[rune]bool, flags, paths []string, args []*syntax.Word) {
	recursive := short['r'] || short['R'] || containsAny(flags, "--recursive")
	if !recursive {
		return
	}
	a.add(RiskSignal{ID: "RM_RECURSIVE_FORCE", Severity: SeverityHigh, Command: "rm", Args: paths, Reason: "recursive delete"})

	for _, p := range paths {
		if isSystemPath(p) {
			a.add(RiskSignal{ID: "DELETE_ROOT_PATH", Severity: SeverityCritical, Command: "rm", Args: []string{p}, Reason: "recursive delete of a system/root path"})
		}
	}
	// Unguarded variable target: `rm -rf "$VAR"` may expand to empty/root.
	for _, w := range args {
		if w.Lit() != "" {
			continue
		}
		if wordHasUnguardedParam(w) {
			a.add(RiskSignal{ID: "UNRESOLVED_VARIABLE_IN_PATH", Severity: SeverityCritical, Command: "rm", Reason: "recursive delete of an unguarded variable path (use ${var:?})"})
			break
		}
	}
}

func (a *Analysis) detectDD(args []string) {
	for _, arg := range args {
		target, ok := strings.CutPrefix(arg, "of=")
		if ok && strings.HasPrefix(target, "/dev/") {
			a.add(RiskSignal{ID: "DD_WRITE_BLOCK_DEVICE", Severity: SeverityCritical, Command: "dd", Args: []string{arg}, Reason: "dd writing to a block device"})
		}
	}
}

func (a *Analysis) detectRecursivePerm(name, id string, short map[rune]bool, flags, paths []string) {
	if !short['R'] && !short['r'] && !containsAny(flags, "--recursive") {
		return
	}
	sev := SeverityMedium
	for _, p := range paths {
		if isSystemPath(p) {
			sev = SeverityCritical
			a.add(RiskSignal{ID: id, Severity: sev, Command: name, Args: []string{p}, Reason: "recursive permission change on a system path"})
			return
		}
	}
	a.add(RiskSignal{ID: id, Severity: sev, Command: name, Args: paths, Reason: "recursive permission change"})
}

func (a *Analysis) detectServiceControl(name string, args []string) {
	if containsAny(args, "stop", "restart", "disable", "mask", "kill") {
		a.add(RiskSignal{ID: "SYSTEMCTL_STOP_SERVICE", Severity: SeverityHigh, Command: name, Args: args, Reason: "service stop/restart/disable"})
	}
}

func (a *Analysis) detectKubectl(args []string) {
	switch {
	case containsAny(args, "delete"):
		a.add(RiskSignal{ID: "KUBECTL_DELETE", Severity: SeverityHigh, Command: "kubectl", Args: args, Reason: "kubectl delete"})
	case containsAny(args, "apply") && hasRemoteURL(args):
		a.add(RiskSignal{ID: "KUBECTL_APPLY_REMOTE", Severity: SeverityHigh, Command: "kubectl", Args: args, Reason: "kubectl apply from a remote URL"})
	case containsAny(args, "scale", "drain", "cordon"):
		a.add(RiskSignal{ID: "KUBECTL_DELETE", Severity: SeverityHigh, Command: "kubectl", Args: args, Reason: "kubectl mutating operation"})
	}
}

func (a *Analysis) detectHelm(args []string) {
	if containsAny(args, "uninstall", "delete") {
		a.add(RiskSignal{ID: "HELM_UNINSTALL", Severity: SeverityHigh, Command: "helm", Args: args, Reason: "helm uninstall"})
	}
}

func (a *Analysis) detectDocker(args, flags []string) {
	if containsAny(args, "rm") && (containsAny(flags, "-f", "--force")) {
		a.add(RiskSignal{ID: "DOCKER_RM_FORCE", Severity: SeverityHigh, Command: "docker", Args: args, Reason: "docker force-remove"})
	}
	if containsAny(args, "prune") {
		a.add(RiskSignal{ID: "DOCKER_SYSTEM_PRUNE", Severity: SeverityHigh, Command: "docker", Args: args, Reason: "docker prune"})
	}
}

func (a *Analysis) detectAWS(args []string) {
	switch {
	case containsAny(args, "rm") && containsAny(args, "--recursive"):
		a.add(RiskSignal{ID: "AWS_S3_RM_RECURSIVE", Severity: SeverityHigh, Command: "aws", Args: args, Reason: "aws s3 recursive delete"})
	case containsAny(args, "terminate-instances"):
		a.add(RiskSignal{ID: "AWS_EC2_TERMINATE", Severity: SeverityHigh, Command: "aws", Args: args, Reason: "aws ec2 terminate"})
	}
}

func (a *Analysis) detectGcloud(args []string) {
	if containsAny(args, "delete") {
		a.add(RiskSignal{ID: "GCLOUD_DELETE", Severity: SeverityHigh, Command: "gcloud", Args: args, Reason: "gcloud destructive delete"})
	}
}

// detectForkBomb catches the classic :(){ :|:& };: defined as a function named ":".
func (a *Analysis) detectForkBomb(name string) {
	if name == ":" {
		a.add(RiskSignal{ID: "FORK_BOMB", Severity: SeverityCritical, Command: name, Reason: "possible fork bomb"})
	}
}

// inspectPipe flags piping a remote download straight into a shell (curl|sh).
func (a *Analysis) inspectPipe(b *syntax.BinaryCmd) {
	left := stmtCommandName(b.X)
	right := stmtCommandName(b.Y)
	if isDownloader(left) && isShell(right) {
		a.add(RiskSignal{ID: "CURL_PIPE_SHELL", Severity: SeverityCritical, Command: left, Reason: "remote download piped into a shell"})
	}
}

// inspectCmdSubst flags command substitution, and eval $(curl…) as remote-exec.
func (a *Analysis) inspectCmdSubst(c *syntax.CmdSubst) {
	a.add(RiskSignal{ID: "COMMAND_SUBSTITUTION", Severity: SeverityMedium, Reason: "command substitution"})
	for _, st := range c.Stmts {
		if isDownloader(stmtCommandName(st)) {
			a.add(RiskSignal{ID: "REMOTE_DOWNLOAD_EXECUTE", Severity: SeverityCritical, Reason: "remote download inside a command substitution"})
			return
		}
	}
}

// inspectRedirect flags redirection that overwrites a block device.
func (a *Analysis) inspectRedirect(r *syntax.Redirect) {
	if r.Word == nil {
		return
	}
	target := r.Word.Lit()
	if isBlockDevicePath(target) {
		a.add(RiskSignal{ID: "DD_WRITE_BLOCK_DEVICE", Severity: SeverityCritical, Reason: "redirect overwrites a block device"})
	}
}

// --- helpers ---------------------------------------------------------------

// shortFlagChars collects the individual letters of single-dash flags, expanding
// combined forms like "-rf" into {r,f}. Long flags ("--force") are ignored here.
func shortFlagChars(flags []string) map[rune]bool {
	set := make(map[rune]bool)
	for _, f := range flags {
		if !strings.HasPrefix(f, "-") || strings.HasPrefix(f, "--") {
			continue
		}
		for _, r := range strings.TrimPrefix(f, "-") {
			set[r] = true
		}
	}
	return set
}

// splitArgs returns flags, path-like args, and the raw textual form of every arg.
func splitArgs(words []*syntax.Word) (flags, paths, raw []string) {
	for _, w := range words {
		text := wordText(w)
		raw = append(raw, text)
		switch {
		case strings.HasPrefix(text, "-"):
			flags = append(flags, text)
		case text != "":
			paths = append(paths, text)
		}
	}
	return flags, paths, raw
}

// wordText renders a word to a best-effort string: literal parts verbatim,
// parameter expansions as $NAME, others as their literal where available.
func wordText(w *syntax.Word) string {
	if lit := w.Lit(); lit != "" {
		return lit
	}
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, dp := range p.Parts {
				if lit, ok := dp.(*syntax.Lit); ok {
					b.WriteString(lit.Value)
				} else if pe, ok := dp.(*syntax.ParamExp); ok && pe.Param != nil {
					b.WriteString("$" + pe.Param.Value)
				}
			}
		case *syntax.ParamExp:
			if p.Param != nil {
				b.WriteString("$" + p.Param.Value)
			}
		}
	}
	return b.String()
}

// wordHasUnguardedParam reports whether a word contains a parameter expansion
// without a ":?" error guard (e.g. "$VAR" or "${VAR}" but not "${VAR:?}").
func wordHasUnguardedParam(w *syntax.Word) bool {
	var found bool
	syntax.Walk(w, func(n syntax.Node) bool {
		pe, ok := n.(*syntax.ParamExp)
		if !ok {
			return true
		}
		// A ":?" guard sets Exp with an error operator; treat any Exp as guarded.
		if pe.Exp == nil {
			found = true
		}
		return true
	})
	return found
}

// stmtCommandName returns the command name of a statement's simple command.
func stmtCommandName(st *syntax.Stmt) string {
	if st == nil {
		return ""
	}
	call, ok := st.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return ""
	}
	return call.Args[0].Lit()
}

func isSystemPath(p string) bool {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return true // "/" trimmed to ""
	}
	for _, sp := range systemPaths {
		if p == strings.TrimRight(sp, "/") {
			return true
		}
	}
	return false
}

func isDownloader(name string) bool {
	switch name {
	case "curl", "wget", "fetch":
		return true
	}
	return false
}

func isShell(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "ksh", "dash":
		return true
	}
	return false
}

func hasRemoteURL(args []string) bool {
	for _, a := range args {
		if strings.HasPrefix(a, "http://") || strings.HasPrefix(a, "https://") {
			return true
		}
	}
	return false
}

func containsAny(haystack []string, needles ...string) bool {
	for _, n := range needles {
		if slices.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func appendUnique(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

func appendUniqueAll(s, vs []string) []string {
	for _, v := range vs {
		s = appendUnique(s, v)
	}
	return s
}
