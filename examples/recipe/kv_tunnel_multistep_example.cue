// Example: kv_tunnel across multiple steps (same cue-exec run).
//
// With recipe.defaults.kv_tunnel: true, SSH targets share one operator-side stepkv
// session for the whole run — keys written in step N are visible in step M on the
// same host. Use a per-host key (here: mstep_$HONEY_HOST_NAME) so parallel hosts do
// not stomp each other's values.
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
				K="mstep_${HONEY_HOST_NAME}"
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
				K="mstep_${HONEY_HOST_NAME}"
				V="$(curl -fsS -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/${K}")"
				echo "step 2: read ${K} -> ${V}"
				case "$V" in step1-*) ;; *) echo "expected value to start with step1-"; exit 1 ;; esac
			"""
		},
		{
			host: "*"
			command: """
				set -e
				K="mstep_${HONEY_HOST_NAME}"
				curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" -H 'Content-Type: text/plain; charset=utf-8' \\
				  --data-binary "step3-done" "${HONEY_KV_URL}/v1/kv/${K}"
				echo "step 3: final $(curl -fsS -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/${K}")"
			"""
		},
	]
}
