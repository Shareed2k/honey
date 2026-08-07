// Controller-mode RTSP remediation: an LLM probes a camera stream and restarts
// the proxy stack ONLY if the stream is down, then re-probes to confirm recovery.
//
// This is the adaptive, conditional version of rtsp_healthcheck_restart.cue (which
// hard-wires probe -> restart-on-failure via a graph trigger_rule). Here the model
// decides: probe first, and only if the probe fails does it request approval and
// run stop -> start -> probe-again. A healthy stream ends with no restart.
//
// The LLM's action space is exactly these three operator-authored steps — it can
// only probe, stop, or start the declared stack; it cannot invent commands. Every
// step still runs through honey's gates. Restart is destructive, so the controller
// is told to request_approval before any stop/start (a real terminal answers y/N;
// a non-interactive run auto-denies, and the model then settles the task as skipped).
//
// Setup (once):
//   plugins.enabled: true in honey config, then install rtsp_probe by copying it in
//   (docker-runtime plugins have no plugin.wasm, so `honey plugins install` — WASM
//   only — does not apply):
//     cp -r examples/plugins/rtsp_probe ~/.config/honey/plugins/
//   Seal your Dokploy API key and paste the ref into both http steps' secrets:
//     printf '%s' '<DOKPLOY_API_KEY>' | honey secrets seal --cue-key DOKPLOY_API_KEY
//   Replace CAMERA_HOST / DOKPLOY_HOST / REPLACE_WITH_COMPOSE_ID with your values.
//
// Run (needs >=1 inventory host for the search; every step runs on the operator):
//   honey cue-validate examples/recipe/controller_rtsp_restart.cue
//   honey cue-exec examples/recipe/controller_rtsp_restart.cue <inventory-host>            # dry-run, no LLM call
//   OPENAI_API_KEY=… honey cue-exec examples/recipe/controller_rtsp_restart.cue <inventory-host> --execute
//
// Model resolves as controller.model -> $OPENAI_MODEL -> "gpt-4o"; point
// $OPENAI_BASE_URL at any OpenAI-compatible endpoint. Use a capable model — a weak
// one may skip the re-probe or restart without approval.
recipe: {
	name: "controller-rtsp-restart"
	type: "controller"
	controller: {
		max_turns: 14
		system_prompt: "You keep a camera's RTSP stream healthy. Restarting the proxy stack (stop then start) interrupts the camera and is destructive, so ALWAYS call request_approval before any stop or start. First call run_probe. If it succeeds, the stream is delivering video: settle stream_probed=completed and stream_restored=completed (note that no restart was needed) and finish. If run_probe fails, the stream is down: request approval, then run_stop, then run_start, then call run_probe again to confirm video has returned before settling stream_restored. If approval is denied, settle stream_restored=skipped."
	}
	tasks: [
		{
			name:        "stream_probed"
			description: "the camera RTSP stream has been probed to determine whether it is currently delivering video"
		},
		{
			name:        "stream_restored"
			description: "if the probe showed the stream is down, the proxy stack was restarted (stop then start) and a follow-up probe confirms video is flowing again; if the stream was already healthy, no restart was performed"
		},
	]
	steps: [
		{
			id:          "probe"
			description: "check whether the camera RTSP stream is delivering video; exits non-zero if the stream is dead, unreachable, or frozen"
			host:        "_"
			// A few retries so a transient network blip does not read as "down".
			retry: {attempts: 3, delay_ms: 5000, backoff: "fixed"}
			plugin: {
				id:     "rtsp_probe"
				action: "check"
				config: {
					input: "rtsp://CAMERA_HOST:8554/stream"
					// transport/timeout_us use plugin defaults (tcp, 15s socket timeout).
				}
			}
		},
		{
			id:          "stop"
			description: "stop the camera proxy compose stack (destructive — interrupts the camera; only when the probe has failed and approval is granted)"
			host:        "_"
			retry: {attempts: 3, delay_ms: 2000, backoff: "exponential"}
			// Replace with your sealed ref: honey secrets seal --cue-key DOKPLOY_API_KEY
			secrets: {DOKPLOY_API_KEY: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}
			http: {
				method:  "POST"
				url:     "http://DOKPLOY_HOST:3010/api/compose.stop"
				timeout: "20s"
				headers: {
					"x-api-key":    "{{ .env.DOKPLOY_API_KEY }}"
					"Content-Type": "application/json"
				}
				body:          "{\"composeId\":\"REPLACE_WITH_COMPOSE_ID\"}"
				expect_status: [200, 201, 202, 204]
			}
		},
		{
			id:          "start"
			description: "start the camera proxy compose stack again (run after stop to bring the camera back)"
			host:        "_"
			retry: {attempts: 3, delay_ms: 2000, backoff: "exponential"}
			secrets: {DOKPLOY_API_KEY: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}
			http: {
				method:  "POST"
				url:     "http://DOKPLOY_HOST:3010/api/compose.start"
				timeout: "20s"
				headers: {
					"x-api-key":    "{{ .env.DOKPLOY_API_KEY }}"
					"Content-Type": "application/json"
				}
				body:          "{\"composeId\":\"REPLACE_WITH_COMPOSE_ID\"}"
				expect_status: [200, 201, 202, 204]
			}
		},
	]
}
