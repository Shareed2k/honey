// Remote recipe: spot heavy CPU and memory consumers on Linux hosts (GNU ps).
//
// Uses `ps` with `%cpu`, `%mem`, and `rss` sort keys (common on glibc-based
// distros: Ubuntu, Debian, RHEL, Amazon Linux 2+). If `ps` errors, the step
// still exits 0 so other hosts continue (`|| true` on the pipeline tail).
//
// Tuning: change the `head` count (default 16 lines = header + 15 processes)
// or duplicate steps with different `host` / `re:` patterns.
//
// Validate:
//   honey cue-validate examples/recipe/high_load_processes.cue
// Plan (dry-run):
//   honey cue-exec examples/recipe/high_load_processes.cue "<search>"
// Run:
//   honey cue-exec examples/recipe/high_load_processes.cue "<search>" --execute
//
// Targeting: `host: "*"` runs on every search row with PrimaryIP. Narrow with
// a name substring, `re:…`, or a single host/IP per step as needed.
recipe: {
	name: "inspect-high-cpu-mem-processes"

	steps: [
		{
			host: "*"
			command: "echo \"=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) load / memory ===\" && (uptime; echo; command -v free >/dev/null 2>&1 && free -h || echo \"skip: no free\")"
		},
		{
			host: "*"
			command: "echo \"=== Top processes by CPU% (GNU ps) ===\" && (ps -eo pid,user,%cpu,%mem,rss,etime,args --sort=-%cpu 2>/dev/null | head -16 || echo \"skip: ps --sort not supported\")"
		},
		{
			host: "*"
			command: "echo \"=== Top processes by RSS (resident KB) ===\" && (ps -eo pid,user,%cpu,%mem,rss,etime,args --sort=-rss 2>/dev/null | head -16 || echo \"skip: ps --sort not supported\")"
		},
				{
			host: "_"
			notify: {
				notify_subject: "Honey AI summary"
				services: { http: {} }
			}
			summarize: {
				prompt: """
Summarize the host listing in 3–5 bullet points. Note any missing output or failures.
"""
				model: "models/gemini-3.1-pro-preview"
				// max_input_chars: 100000
				// max_output_tokens: 800
			}
		},
	]
}
