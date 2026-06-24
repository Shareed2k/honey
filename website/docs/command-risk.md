---
id: command-risk
title: Command Risk Engine
slug: /command-risk
---

Honey does **not** trust an LLM to judge whether a shell command is dangerous.
Instead it runs a deterministic **Command Risk Engine** in front of every
command and script step: it parses the command to an AST, derives risk signals
with fixed rules, and lets an [OPA policy](/authorization) make the contextual
decision. A local LLM may *explain* the risk, but never decides.

The decision order is fixed and load-bearing:

> **built-in critical deny → OPA policy → (LLM advisory, explanation only)**

An LLM suggestion can never turn a deny into an allow.

## How it works

1. **Parse** — the command is parsed with `mvdan.cc/sh` (no execution).
2. **Detect** — deterministic rules emit `RiskSignal`s with a severity
   (`low`/`medium`/`high`/`critical`).
3. **Built-in critical deny** — critical patterns are denied immediately, even
   with no OPA configured (safety default).
4. **OPA** — when a policy is configured, non-critical commands are decided by
   the `command_exec` action with full context (actor, target env, host vars,
   dry-run state).

Denied hosts are **skipped** (shown as skipped in the run output); the rest
proceed.

## Risk signals

A representative set of detectors (severity in parentheses):

- **Critical** (built-in hard deny): `rm -rf /` and other root paths
  (`DELETE_ROOT_PATH`), `rm -rf "$VAR"` with an unguarded variable
  (`UNRESOLVED_VARIABLE_IN_PATH`), `curl … | sh` (`CURL_PIPE_SHELL`),
  `eval "$(curl …)"` (`REMOTE_DOWNLOAD_EXECUTE`), `dd of=/dev/sdX`
  (`DD_WRITE_BLOCK_DEVICE`), `mkfs.*` (`MKFS_FILESYSTEM`), `chmod -R … /`
  (`CHMOD_RECURSIVE_SYSTEM_PATH`), fork bomb (`FORK_BOMB`).
- **High**: `sudo`, `systemctl stop/restart`, `kubectl delete`, `helm uninstall`,
  `docker system prune`, `aws s3 rm --recursive`, `aws ec2 terminate-instances`,
  `gcloud … delete`, `rm -rf <path>`.
- **Medium**: command substitution, package removal, recursive chmod/chown on
  non-system paths, an unparseable command.

### Examples

What a few commands resolve to (signal ID and severity):

| Command | Signal | Severity |
| --- | --- | --- |
| `rm -rf /var/cache/*` | `RM_RECURSIVE_FORCE` | high |
| `rm -rf /` | `DELETE_ROOT_PATH` | **critical** |
| `rm -rf "$DIR"` | `UNRESOLVED_VARIABLE_IN_PATH` | **critical** |
| `curl https://x.sh \| sh` | `CURL_PIPE_SHELL` | **critical** |
| `eval "$(curl https://x.sh)"` | `REMOTE_DOWNLOAD_EXECUTE` | **critical** |
| `dd if=img of=/dev/sda` | `DD_WRITE_BLOCK_DEVICE` | **critical** |
| `mkfs.ext4 /dev/sdb1` | `MKFS_FILESYSTEM` | **critical** |
| `chmod -R 777 /etc` | `CHMOD_RECURSIVE_SYSTEM_PATH` | **critical** |
| `sudo systemctl stop nginx` | `SUDO_PRIVILEGE_ESCALATION` + `SYSTEMCTL_STOP_SERVICE` | high |
| `kubectl delete pod x -n prod` | `KUBECTL_DELETE` | high |
| `helm uninstall app` | `HELM_UNINSTALL` | high |
| `docker system prune -af` | `DOCKER_SYSTEM_PRUNE` | high |
| `aws s3 rm s3://b --recursive` | `AWS_S3_RM_RECURSIVE` | high |
| `apt-get remove nginx` | `PACKAGE_REMOVE` | medium |
| `echo $(hostname)` | `COMMAND_SUBSTITUTION` | medium |

## Interpreter-aware analysis

A recipe `command`/`script` step may declare an `interpreter` (e.g.
`interpreter: "python3"`). The risk engine picks the parser to match — it never
shell-parses a non-shell body:

- **Shell** (`sh`/`bash`/`zsh`/`ksh`/`dash`, or unset) → parsed with `mvdan/sh`
  (everything above).
- **Python** (`python3`/`python`) → parsed with [gpython](https://github.com/go-python/gpython)
  into a Python AST (still **no execution**). Detectors walk the AST; shell
  strings handed to `os.system` / `subprocess.*` recurse back into the shell
  analyzer, so a python wrapper around a dangerous command is still caught.
  gpython implements a pre-3.6 grammar, so modern syntax (f-strings, walrus,
  `match`) cannot be parsed; such a step yields a **low** `PYTHON_PARSE_INCOMPLETE`
  note (analysis skipped, *not* a risk) and the decision is left to OPA.
- **Other** interpreters (`node`, `ruby`, …) → not parsed (no bogus signals);
  the decision is left entirely to OPA, which receives the interpreter.

The step's interpreter is always passed to policy as `input.command.interpreter`.

Python detectors:

| Python | Signal | Severity |
| --- | --- | --- |
| `os.system("rm -rf /")` | `DELETE_ROOT_PATH` (via shell recursion) | **critical** |
| `subprocess.run(["rm","-rf","/etc"])` | `DELETE_ROOT_PATH` | **critical** |
| `shutil.rmtree("/")` | `PYTHON_RMTREE_SYSTEM_PATH` | **critical** |
| `shutil.rmtree("/tmp/x")` | `PYTHON_RMTREE` | high |
| `os.remove("/etc/passwd")` | `PYTHON_DELETE_SYSTEM_PATH` | **critical** |
| `open("/dev/sda","wb")` | `DD_WRITE_BLOCK_DEVICE` | **critical** |
| `eval(user_input)` | `PYTHON_DYNAMIC_EXEC` | medium |
| `os.system(cmd)` (runtime-built) | `PYTHON_DYNAMIC_SHELL_EXEC` | medium |

## Policy input

The `command_exec` action receives:

```json
{
  "action": "command_exec",
  "actor": "alice@example.com",
  "command": {
    "raw": "kubectl delete pod x -n prod",
    "max_severity": "high",
    "interpreter": "",
    "riskSignals": [{"id": "KUBECTL_DELETE", "severity": "high", "reason": "kubectl delete"}],
    "detected": {"commands": ["kubectl"], "flags": [], "paths": []}
  },
  "target": {"name": "prod-1", "provider": "k8s", "env": "prod", "host_vars": {"tier": "prod"}},
  "execution": {"recipe": "deploy", "dry_run": false}
}
```

Example policy — block high-risk commands on prod, require approval otherwise:

```rego
package honey
import rego.v1

default allow := true

allow := false if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.env == "prod"
}
deny_reason := "high-risk command on prod" if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.env == "prod"
}
```

### More policy patterns

**Command allowlist** — deny anything outside a permitted set of binaries:

```rego
package honey
import rego.v1

default allow := true

allowed_commands := {"systemctl", "journalctl", "kubectl", "helm", "echo", "cat"}

allow := false if {
	input.action == "command_exec"
	some cmd in input.command.detected.commands
	not allowed_commands[cmd]
}
deny_reason := sprintf("command not in allowlist: %v", [input.command.detected.commands]) if {
	input.action == "command_exec"
	some cmd in input.command.detected.commands
	not allowed_commands[cmd]
}
```

**Prod guard via host vars** — block high-risk commands on prod-tier hosts
(the host's resolved [inventory](/authorization#inventory-as-policy-data)
variables arrive as `input.target.host_vars`):

```rego
package honey
import rego.v1

default allow := true

allow := false if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.host_vars.tier == "prod"
}
deny_reason := "high-risk command blocked on prod tier" if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	input.target.host_vars.tier == "prod"
}
```

**Dry-run bypass** — never block during a dry-run / `honey exec --check`, so
operators can preview risk without a hard deny:

```rego
package honey
import rego.v1

default allow := true

allow if {
	input.action == "command_exec"
	input.execution.dry_run == true
}
```

**Require approval for high-risk** — hold the run for a second person instead of
denying outright (see the [approval flow](/authorization#approval-flow)):

```rego
package honey
import rego.v1

default allow := true

decision := "require_approval" if {
	input.action == "command_exec"
	input.command.max_severity == "high"
	not input.execution.approved
}
```

**Interpreter gate** — block Python steps on prod (the interpreter is in the
input even for non-shell steps the engine does not parse):

```rego
package honey
import rego.v1

default allow := true

allow := false if {
	input.action == "command_exec"
	input.command.interpreter == "python3"
	input.target.env == "prod"
}
deny_reason := "python steps are not allowed on prod" if {
	input.action == "command_exec"
	input.command.interpreter == "python3"
	input.target.env == "prod"
}
```

## CLI: `honey exec --check`

Analyze a command's risk without running it:

```bash
honey exec "web-*" --check "rm -rf /var/cache/*"
```

Output lists the detected signals and max severity, and — when
`HONEY_POLICY_DIR` is set — the per-target OPA decision. Add `--shellcheck` to
also surface [ShellCheck](https://www.shellcheck.net/) warnings (optional; the
binary is used when present, skipped otherwise). A critical or denied command
exits non-zero. Nothing is executed.

A built-in critical pattern is denied even with no policy configured:

```text
$ honey exec "web-*" --check "rm -rf /"
Command: rm -rf /
Risk: critical
  - [high] RM_RECURSIVE_FORCE: recursive delete
  - [critical] DELETE_ROOT_PATH: recursive delete of a system/root path
Detected: commands=[rm] flags=[-rf] paths=[/]
Decision: DENY (built-in critical: recursive delete of a system/root path)
```

A non-critical command with no policy configured is allowed (signals are still
reported for visibility):

```text
$ honey exec "web-*" --check "apt-get remove nginx"
Command: apt-get remove nginx
Risk: medium
  - [medium] PACKAGE_REMOVE: package removal
Detected: commands=[apt-get] flags=[] paths=[remove nginx]
Decision: allow (no policy configured; only built-in critical patterns deny)
```

With `HONEY_POLICY_DIR` set, each target is evaluated by the `command_exec`
policy and the per-target verdict is printed:

```text
$ HONEY_POLICY_DIR=/etc/honey/policies honey exec "prod-1" --check "kubectl delete pod x -n prod"
Command: kubectl delete pod x -n prod
Risk: high
  - [high] KUBECTL_DELETE: kubectl delete
Detected: commands=[kubectl] flags=[] paths=[delete pod x -n prod]
Policy[prod-1]: deny — high-risk command on prod
```

### Checking a whole recipe (`cue-exec --check`)

`honey exec --check` analyzes a single command. To preview the risk of an entire
[recipe](/cue-recipes) without running it, add `--check` to `cue-exec`:

```bash
honey cue-exec deploy.cue "prod-*" --check
```

It analyzes every `command` and `script` step (the same per-step analysis and
`command_exec` decision the web dry-run produces), prints one block per step,
executes nothing, and exits non-zero if any step is a built-in critical pattern
or denied by policy. With `HONEY_POLICY_DIR` set, each step also shows its policy
verdict.

```text
$ HONEY_POLICY_DIR=/etc/honey/policies honey cue-exec deploy.cue "prod-1" --check
Step 0 [command] host=prod-*: systemctl restart app
  Risk: high
    - [high] SYSTEMCTL_STOP_SERVICE: service stop/restart/disable
  Policy: require_approval
Step 1 [command] host=prod-*: rm -rf /var/cache/app/*
  Risk: high
    - [high] RM_RECURSIVE_FORCE: recursive delete
  Policy: allow
```

## Dry-run review

A cue-exec dry-run (`execute: false`) returns a `risk_assessment` array
alongside the plan — one entry per command/script step with its analysis and
(when OPA is configured) the policy decision — so a UI can show a review before
the operator confirms. This is the **web** surface; the CLI prints the same
per-step preview via [`cue-exec --check`](#checking-a-whole-recipe-cue-exec---check).

```json
{
  "plan": "...",
  "risk_assessment": [
    {
      "step_index": 0,
      "kind": "command",
      "host": "prod-1",
      "command": "kubectl delete pod x -n prod",
      "analysis": {
        "signals": [
          {"id": "KUBECTL_DELETE", "severity": "high", "command": "kubectl", "reason": "kubectl delete"}
        ],
        "detected": {"commands": ["kubectl"], "flags": [], "paths": ["delete", "pod", "x", "-n", "prod"]},
        "max_severity": "high",
        "critical": false
      },
      "decision": {
        "Allow": false,
        "DenyReason": "high-risk command on prod",
        "Decision": "deny",
        "Requires": null
      }
    }
  ]
}
```

The nested `decision` object mirrors the Go `policy.Decision` struct (no JSON
tags), so its keys are capitalized: `Allow`, `DenyReason`, `Decision`,
`Requires`. It is omitted when no policy is configured.

## LLM advisory (optional, never authoritative)

Set `HONEY_RISK_LLM=1` to attach an advisory classification from a model
(via the existing AI client — point `OPENAI_BASE_URL` at a local Ollama /
llama.cpp endpoint for a fully local model). It produces `{risk, reasons,
explanation}` for display only and is **excluded** from every allow/deny
decision. The advisor is a pluggable seam (`Advisor` interface), so a future
local ONNX / trained command-risk classifier can replace the LLM without
changing callers.

## In a recipe

Gating is automatic: every `command` and `script` step in a [CUE
recipe](/cue-recipes) passes through the engine before it runs. No special
syntax is needed — the same step works with or without a policy.

```cue
recipe: {
	name: "cache-clear"
	steps: [
		{
			host:    "web-*"
			command: "rm -rf /var/cache/app/*"
		},
	]
}
```

When a policy denies `command_exec` for a given host, that host is **skipped**
(reported as skipped in the run output) while the remaining hosts proceed. A
built-in critical pattern (e.g. `rm -rf /`) is denied on every host regardless
of policy.

## Escape hatch

Set `HONEY_RISK_DISABLE=1` to bypass the gate entirely (including built-in
critical denies) for a trusted run.

## Scope

The gate covers `command` and `script` steps (shell). With no OPA configured,
only the built-in critical patterns deny; everything else runs, with signals
attached to results for visibility.
