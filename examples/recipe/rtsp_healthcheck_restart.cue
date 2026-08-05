// Probe an RTSP camera via the rtsp_probe ffmpeg plugin; if it is not delivering
// video, restart the proxy container through the Dokploy REST API (http step).
//
// Setup (once):
//   plugins.enabled: true in honey config, then install the plugin by copying the
//   directory into your plugins root (docker-runtime plugins have no plugin.wasm,
//   so `honey plugins install` — which is WASM-only — does not apply):
//     cp -r examples/plugins/rtsp_probe ~/.config/honey/plugins/
//   Seal the Dokploy API key into a secure:v1 ref and paste it into the restart
//   step's `secrets` below:
//     printf '%s' '<DOKPLOY_API_KEY>' | honey secrets seal --cue-key DOKPLOY_API_KEY
//
// Run (needs >=1 inventory host for the search; both steps run on the operator):
//   honey cue-validate examples/recipe/rtsp_healthcheck_restart.cue
//   honey cue-exec examples/recipe/rtsp_healthcheck_restart.cue <inventory-host>            # dry-run
//   honey cue-exec examples/recipe/rtsp_healthcheck_restart.cue <inventory-host> --execute  # act
//
// When the probe fails and the restart fires, the run exits non-zero (the probe
// step is genuinely failed) — that doubles as the "camera down, action taken" signal.
recipe: {
	name: "rtsp-healthcheck-restart"
	type: "graph"
	steps: [
		{
			id:   "probe"
			host: "_"
			// A few retries so a transient network blip does not restart the container.
			retry: {attempts: 3, delay_ms: 5000, backoff: "fixed"}
			plugin: {
				id:     "rtsp_probe"
				action: "check"
				config: {
					input: "rtsp://192.168.88.243:18554/balcony/stream"
					// sample/transport/timeout_us use the plugin defaults (read 3s
					// of the video stream over tcp, 15s socket timeout).
				}
			}
		},
		{
			id:           "restart"
			host:         "_"
			depends:      ["probe"]
			trigger_rule: "one_failed" // run ONLY if the probe failed
			// Retry the restart on a transient Dokploy 5xx (HTTP-aware backoff).
			retry: {attempts: 3, delay_ms: 2000, backoff: "exponential"}
			// Replace with your sealed ref: honey secrets seal --cue-key DOKPLOY_API_KEY
			secrets: {DOKPLOY_API_KEY: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}
			http: {
				method:  "POST"
				url:     "http://192.168.88.243:3010/api/docker.restartContainer"
				timeout: "15s" // override the 30s default
				headers: {
					"x-api-key":    "{{ .env.DOKPLOY_API_KEY }}"
					"Content-Type": "application/json"
				}
				body:          "{\"containerId\":\"tools-reolinkproxy-txkl4c\"}"
				expect_status: [200]
			}
		},
	]
}
