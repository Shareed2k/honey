// Example: per-step hooks run after each host's main command/script result.
// `where: "local"` runs on the machine running `honey cue-exec` (operator / CI) — trusted recipes only.
// `where: "remote"` runs a second SSH command on the same target (reuse step run_as unless hook.run_as is set).

recipe: {
	name: "hooks-example"
	defaults: { kv_tunnel: true } // optional: reverse-forward loopback KV to every SSH command/script step
	steps: [
		{
			host:    "*"
			command: "hostname"
			// kv_tunnel: true // optional: sets HONEY_KV_URL + HONEY_KV_TOKEN on remote for this step
			hooks: {
				on_success: {where: "local", command: "kubectl get pods --all-namespaces -o custom-columns=NAME:.metadata.name,UID:.metadata.uid"}
				on_failure: {where: "remote", command: "logger honey-hook-failed"}
			}
		},
	]
}
