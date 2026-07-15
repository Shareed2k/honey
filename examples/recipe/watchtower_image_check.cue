// Check every matched server for newer Docker images (monitor-only) using the
// watchtower docker-runtime plugin, and alert when a new image is found.
//
//   honey cue-exec --execute examples/recipe/watchtower_image_check.cue 'prod-*'
//
// Requires: plugins.enabled: true and the watchtower plugin installed
// (examples/plugins/watchtower). Each matched host needs SSH access (already
// used by honey) and a Docker daemon — nothing else is installed on it: honey
// stages the honey-plugin-init shim to /tmp on the host over SSH and runs the
// watchtower container on that host's own daemon, so watchtower inspects the
// host's containers (its /var/run/docker.sock is mounted, per plugin.yaml).
//
// Reaching the remote Docker socket over SSH needs either the SSH user to be
// in the host's `docker` group, or (as set below) `run_as: "root"` — which
// routes the connection through `sudo -n -u root -- docker system dial-stdio`
// instead of a raw socket forward, and only needs passwordless sudo for the
// SSH user, not docker-group membership. Drop run_as if your SSH user is
// already in `docker` on every matched host.
//
// How the "new image" alert is delivered: export watchtower's own shoutrrr
// config in honey's environment before running (they reach the container via
// the plugin's allowed_env) —
//
//   export WATCHTOWER_NOTIFICATIONS=shoutrrr
//   export WATCHTOWER_NOTIFICATION_URL='telegram://<bot-token>@telegram?chats=<chat-id>'
//   export WATCHTOWER_NOTIFICATION_REPORT=true
//
// watchtower sends the notification itself, only when a newer image exists.
// report-stale's honey notify: block is an OPTIONAL per-run summary via
// honey's own channels (HONEY_NOTIFY_* env) — omit or keep as you like.
//
// host targeting:
//   'prod-*' / any real host  -> runs on THAT host's Docker daemon (this recipe)
//   host: "_"                 -> would run on the operator's local daemon instead
//
// check-images uses the "check_json" action so report-stale can extract
// structured fields (below); swap to the plain-text "check" action if you
// only want the human-readable log in results, dropping report-stale
// entirely. To actually pull+restart on new images, switch to the "update"
// action instead (opt-in; this recipe is monitor-only by default).
//
// Second step ("report-stale") shows consuming check_json's structured JSON
// (see plugin.cue's check_json comment for the exact shape) instead of
// grepping check's plain text: env_from.extract runs a real JQ filter
// (github.com/itchyny/gojq) per host against check-images' stdout —
// ".report.stale | length" yields a plain number, ".report.stale" yields the
// (re-JSON-encoded) array of stale image records — both land directly in
// shell env vars, no templated:/fromJson needed since env_from already does
// the JSON round-trip. Uses check_json specifically because check's action
// is plain text with nothing to extract this way. env_from is capped at
// 8192 bytes per value; for a host with very many stale images at once,
// switch to plugin.kv_key (64KB cap) + a templated:true downstream step
// instead — see stealth_browser_demo.cue for that pattern.
recipe: {
	name: "watchtower-image-check"
	type: "graph"
	steps: [
		{
			id:     "check-images"
			host:   "*"
			run_as: "root"
			plugin: {
				id:     "watchtower"
				action: "check_json"
				// config: { containers: ["app", "db"] }  // optional; empty = all
				kv_key: "json"
			}
		},
		{
			id:      "report-stale"
			host:    "*"
			depends: ["check-images"]
			env_from: [{
				step: "check-images"
				extract: {
					stale_count:  ".report.stale | length"
					stale_images: ".report.stale"
				}
			}]
			command: """
				echo "watchtower: $stale_count stale image(s) on $(hostname)"
				if [ "$stale_count" -gt 0 ]; then
					echo "$stale_images"
				fi
				"""
			// Optional run summary through honey's own notify channels
			// (HONEY_NOTIFY_HTTP_URL / _SLACK_WEBHOOK_URL / _TELEGRAM_*). No
			// message: set, so the default body is this step's own stdout
			// above (the stale count + image list already printed there).
			// The primary "new image found" alert still comes from
			// watchtower's own shoutrrr config on check-images, not from here.
			notify: {
				notify_subject: "watchtower: stale image report"
			}
		},
	]
}
