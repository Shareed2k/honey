// Kubernetes interception, targetless: an `intercept` step gives a *local*
// command/script egress through an in-cluster agent, with no target pod and
// no SSH access to any host. A targetless session is egress-only (env and
// files both need a target pod, which v1 does not support — see
// website/docs/intercept.md's targetless note). A follow-up step reuses the
// same session via `session_step` instead of paying the deploy cost again.
// See website/docs/intercept.md and website/docs/cue-recipes.md#intercept-steps.
//
//   honey cue-validate examples/recipe/intercept.cue
//   honey cue-exec examples/recipe/intercept.cue
//   honey cue-exec --execute --config /path/to/honey.yaml examples/recipe/intercept.cue
//
// Rules:
// - `intercept` is local-only (no SSH/host fan-out): both steps use
//   `host: "_"`, same as `summarize`/`ai`.
// - Reuse via `session_step` needs a stable step `id`, which linear recipes
//   only allow on steps that also set `when` — so this recipe uses
//   `type: "graph"`. The reusing step then auto-orders after the
//   establishing step; no `depends` is written by hand.
// - The establishing step sets `targetless: true` plus `cluster` and
//   `namespace`; `mode` is optional and, if set, must be `["egress"]` (the
//   only mode a targetless session supports — it defaults to egress when
//   omitted). A `session_step` step must NOT repeat
//   `mode`/`targetless`/`cluster`/`namespace`/`udp`/`env_include`/
//   `env_exclude` — those belong on the establishing step only.
// - `failed_when: "exit_code != 0"` treats the command's numeric exit code
//   as the step's success/failure signal, same as command/script steps.
// - Requires honey config with `intercept.enabled: true` and an
//   `intercept.agent_image` to actually execute; dry-run needs neither.
recipe: {
	name: "intercept-example"
	type: "graph"
	steps: [
		{
			id:   "cluster"
			host: "_"
			intercept: {
				mode:       ["egress"]
				targetless: true
				cluster:    "staging"
				namespace:  "checkout"
				script: """
					echo "checking payments from inside staging/checkout"
					curl -sf http://payments.checkout.svc.cluster.local:8080/healthz
					"""
			}
			failed_when: "exit_code != 0"
		},
		{
			id:   "check_ready"
			host: "_"
			intercept: {
				session_step: "cluster"
				command:      "curl -sf http://payments.checkout.svc.cluster.local:8080/ready"
			}
			failed_when: "exit_code != 0"
		},
	]
}
