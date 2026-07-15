// watchtower docker-runtime plugin: check for (and optionally apply) newer
// images of the containers running on a Docker daemon.
//
// honey overrides the image entrypoint with the honey-plugin-init shim, so
// argv[0] must be watchtower's absolute binary path (/watchtower), not rely on
// the image's own ENTRYPOINT.
//
// Optional config.containers restricts the scan to named containers; the
// default empty list means all running containers on the daemon.

// check: detect newer images and report only — never pulls or restarts
// anything. When WATCHTOWER_NOTIFICATION_URL is set (via allowed_env),
// watchtower sends its own "new image found" notification.
actions: check: {
	#Config: {
		// Required-with-default, not optional (`?`): internal/plugins'
		// evalAction decodes the unified config back to a plain map to apply
		// defaults, and CUE's Decode omits uninstantiated optional fields —
		// an optional field here would silently vanish before the second
		// compile pass, leaving `config.containers` undefined and argv empty.
		containers: [...string] | *[]
	}
	argv: [
		"/watchtower", "--run-once", "--monitor-only", "--cleanup=false",
		for c in config.containers {c},
	]
	output_format: "text"
}

// check_json: same as check (monitor-only, never mutates), but gets
// watchtower's session data as JSON on stdout instead of log lines, for a
// downstream step to consume via output_format: "json" (e.g. env_from
// extract / kv_key + fromJson). Verified end-to-end against a real run
// (beatkind/watchtower 2.3.2): stdout is exactly one JSON document shaped
// like:
//
//   {
//     "entries": [{"level": "info", "message": "Found new <image> image (<id>)", "time": "..."}],
//     "host": "<container id>",
//     "report": {
//       "scanned": [{"name", "imageName", "currentImageId", "latestImageId", "state", ...}],
//       "stale": [...], "updated": [...], "fresh": [...], "failed": [...], "skipped": [...]
//     },
//     "title": "Watchtower updates on <host>"
//   }
//
// report.stale (nonempty) or report.updated (after a real update) is the
// simplest "did anything change" signal; report.scanned always lists every
// container watchtower looked at, state one of Fresh/Stale/Updated/Failed.
//
// How: "json.v1" is one of watchtower's built-in NAMED notification
// templates (like "porcelain.VERSION.summary-no-log") — not a hand-written Go
// template expression (an earlier '{{- ToJSON .Report -}}' attempt rendered
// an empty `{}`; .Report isn't reachable that way — use the named template).
// --notification-url logger:// + --notification-log-stdout print the
// rendered notification to stdout (a stdout-formatting trick, not a real
// notification channel — unrelated to the WATCHTOWER_NOTIFICATION_URL env the
// check/update actions use for a real alert). --no-startup-message is
// required: without it, watchtower prints a second, separate startup-test
// JSON document before the real one, breaking output_format: "json" (the
// combined stdout must be exactly one JSON value) — confirmed by reproducing
// that exact failure before adding the flag.
actions: check_json: {
	#Config: {
		// Required-with-default, not optional (`?`) — see check's #Config comment.
		containers: [...string] | *[]
	}
	argv: [
		"/watchtower", "--run-once", "--monitor-only", "--cleanup=false",
		"--no-startup-message",
		"--notification-url", "logger://",
		"--notification-log-stdout",
		"--notification-report",
		"--notification-template", "json.v1",
		for c in config.containers {c},
	]
	output_format: "json"
}

// update: actually pull newer images and recreate the affected containers
// (--cleanup removes the old images). Opt-in, separate action so a `check`
// recipe can never accidentally mutate running containers.
actions: update: {
	#Config: {
		// Required-with-default, not optional (`?`): internal/plugins'
		// evalAction decodes the unified config back to a plain map to apply
		// defaults, and CUE's Decode omits uninstantiated optional fields —
		// an optional field here would silently vanish before the second
		// compile pass, leaving `config.containers` undefined and argv empty.
		containers: [...string] | *[]
	}
	argv: [
		"/watchtower", "--run-once", "--cleanup",
		for c in config.containers {c},
	]
	output_format: "text"
}
