// Graph recipe: shared stepkv across waves (namespace with HONEY_STEP_ID + HONEY_HOST_NAME).
//
// Dry-run: honey cue-exec examples/recipe/graph_kv_tunnel.cue "*"
// Execute: honey cue-exec examples/recipe/graph_kv_tunnel.cue "*" --execute
recipe: {
	name: "graph-kv-tunnel"
	type: "graph"
	defaults: {
		kv_tunnel: true
	}
	steps: [
		{
			id:      "seed"
			host:    "*"
			command: """
				set -e
				HSAFE=$(printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__')
				K="graph_${HONEY_STEP_ID}_${HSAFE}"
				curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" -H 'Content-Type: text/plain; charset=utf-8' \\
				  --data-binary "seeded" "${HONEY_KV_URL}/v1/kv/${K}"
				echo "seed: wrote ${K}"
				"""
		},
		{
			id:      "restart_a"
			host:    "*"
			depends: ["seed"]
			command: """
				set -e
				HSAFE=$(printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__')
				SEED_K="graph_seed_${HSAFE}"
				K="graph_${HONEY_STEP_ID}_${HSAFE}"
				V="$(curl -fsS -H "Authorization: Bearer ${HONEY_KV_TOKEN}" "${HONEY_KV_URL}/v1/kv/${SEED_K}")"
				echo "seed=${V}"
				curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" -H 'Content-Type: text/plain; charset=utf-8' \\
				  --data-binary "a" "${HONEY_KV_URL}/v1/kv/${K}"
				"""
		},
		{
			id:      "restart_b"
			host:    "*"
			depends: ["seed"]
			command: """
				set -e
				HSAFE=$(printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__')
				K="graph_${HONEY_STEP_ID}_${HSAFE}"
				curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" -H 'Content-Type: text/plain; charset=utf-8' \\
				  --data-binary "b" "${HONEY_KV_URL}/v1/kv/${K}"
				"""
		},
		{
			id:      "verify"
			host:    "*"
			depends: ["restart_a", "restart_b"]
			command: """
				set -e
				HSAFE=$(printf '%s' "$HONEY_HOST_NAME" | tr '/:' '__')
				for S in seed restart_a restart_b; do
				  curl -fsS -H "Authorization: Bearer ${HONEY_KV_TOKEN}" \\
				    "${HONEY_KV_URL}/v1/kv/graph_${S}_${HSAFE}" || exit 1
				done
				echo ok
				"""
		},
	]
}
