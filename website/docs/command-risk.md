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

## Policy input

The `command_exec` action receives:

```json
{
  "action": "command_exec",
  "actor": "alice@example.com",
  "command": {
    "raw": "kubectl delete pod x -n prod",
    "max_severity": "high",
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

## Dry-run review

A cue-exec dry-run (`execute: false`) returns a `risk_assessment` array
alongside the plan — one entry per command/script step with its analysis and
(when OPA is configured) the policy decision — so a UI can show a review before
the operator confirms.

## LLM advisory (optional, never authoritative)

Set `HONEY_RISK_LLM=1` to attach an advisory classification from a model
(via the existing AI client — point `OPENAI_BASE_URL` at a local Ollama /
llama.cpp endpoint for a fully local model). It produces `{risk, reasons,
explanation}` for display only and is **excluded** from every allow/deny
decision. The advisor is a pluggable seam (`Advisor` interface), so a future
local ONNX / trained command-risk classifier can replace the LLM without
changing callers.

## Escape hatch

Set `HONEY_RISK_DISABLE=1` to bypass the gate entirely (including built-in
critical denies) for a trusted run.

## Scope

The gate covers `command` and `script` steps (shell). With no OPA configured,
only the built-in critical patterns deny; everything else runs, with signals
attached to results for visibility.
