// Example: kv_tunnel across multiple steps (same cue-exec run).
//
// SSH: with recipe.defaults.kv_tunnel: true, all steps share one operator-side stepkv
// session — keys from step N are still there in step M on the same host.
//
// Kubernetes pods: cue-exec uses the same operator stepkv as SSH (long-lived exec
// multiplex per pod). Each step command is still a separate kubectl exec.
//
// Use a per-host key (mstep_<sanitized HONEY_HOST_NAME>) so parallel hosts do not clash.
// HONEY_HOST_NAME may contain "/" (k8s rows); KV keys must be a single path segment —
// map "/" and ":" to "_".
//
// Validate:
//   honey cue-validate examples/recipe/kv_tunnel_multistep_example.cue
// Plan / run (curl required on the remote):
//   honey cue-exec examples/recipe/kv_tunnel_multistep_example.cue "<search>"
//   honey cue-exec examples/recipe/kv_tunnel_multistep_example.cue "<search>" --execute
//
recipe: {
	name: "kv-tunnel-multistep-example"

	defaults: {kv_tunnel: true}

	steps: [
		{
			host: "*"
			command: """
				set -e
				HSAFE=$(printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__')
				K="mstep_${HSAFE}"
				curl -fsS -o /dev/null -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/__health"
				curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" -H 'Content-Type: text/plain; charset=utf-8' \\
				  --data-binary "step1-$(hostname)" "${HONEY_KV_URL}/v1/kv/${K}"
				echo "step 1: wrote ${K}"
			"""
		},
		{
			host: "*"
			command: """
				set -e
				HSAFE=$(printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__')
				K="mstep_${HSAFE}"
				V="$(curl -fsS -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/${K}")"
				echo "step 2: read ${K} -> ${V}"
				case "$V" in step1-*) ;; *) echo "expected value to start with step1-"; exit 1 ;; esac
			"""
		},
		{
			host: "*"
			command: """
				set -e
				HSAFE=$(printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__')
				K="mstep_${HSAFE}"
				curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" -H 'Content-Type: text/plain; charset=utf-8' \\
				  --data-binary "step3-done" "${HONEY_KV_URL}/v1/kv/${K}"
				echo "step 3: final $(curl -fsS -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/${K}")"
			"""
		},
	]
}
