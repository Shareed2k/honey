// Demo recipe for the stealth_browser Docker plugin.
//
//   honey cue-exec --execute examples/recipe/stealth_browser_demo.cue <search-filter>
//
// Requires plugins.enabled: true and the stealth_browser plugin installed.
// <search-filter> only needs to match a host for step "check-title" — the
// "fetch-protected-site" step targets host: "_" (local-only), since
// runtime: docker plugins always execute in a container on the operator
// machine and never touch a target host.
//
// fetch-protected-site's full-page JSON output (easily tens of KB) is
// captured via plugin.kv_key into the recipe KV store (65536-byte cap)
// instead of env_from (8192-byte cap, which this recipe hit before kv_key
// existed). check-title then reads it back with templated: true — a Go
// text/template rendered from live KV/prior-step data before the command
// runs, using the same kvGet/fromJson/regexFind functions template: steps
// already have — no $VAR shell plumbing or grep needed.
recipe: {
	name: "stealth-browser-demo"
	type: "graph"
	steps: [
		{
			id:   "fetch-protected-site"
			host: "_"
			plugin: {
				id:     "stealth_browser"
				action: "fetch"
				config: {
					// Standard headless-browser detection test page — no
					// heavy Cloudflare/Turnstile challenge, loads fast, and
					// reports a table of detection signals (webdriver flag,
					// plugins, chrome runtime, etc.) that stealth patches.
					url: "https://bot.sannysoft.com"
				}
				kv_key: "stealth_fetch"
			}
		},
		{
			id:        "check-title"
			host:      "*"
			depends:   ["fetch-protected-site"]
			templated: true
			command: """
				TITLE="{{ regexFind "(?i)<title>.*</title>" (kvGet "stealth_fetch" | fromJson).content }}"
				echo "Fetched page: $TITLE"

				# A missing/empty title usually means a block page or a
				# failed navigation, not the real target page.
				if [ -z "$TITLE" ]; then
					echo "no title found — fetch likely blocked or failed" >&2
					exit 1
				fi
			"""
		}
	]
}
