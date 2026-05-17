// Graph recipe: when uses operator-local recipe KV (requires defaults.kv_tunnel).
recipe: {
	name: "graph-when-kv"
	type: "graph"
	defaults: kv_tunnel: true
	steps: [
		{
			id:      "seed"
			host:    "*"
			command: """
				K="graph_${HONEY_STEP_ID}_${HONEY_HOST_NAME}_ready"
				curl -fsS -X PUT -H "Authorization: Bearer $HONEY_KV_TOKEN" \
				  -d "1" "$HONEY_KV_URL/v1/kv/${K}"
				"""
		},
		{
			id:      "follow"
			host:    "*"
			depends: ["seed"]
			when:    "kv_has('graph_seed_' + host.name + '_ready')"
			command: "echo follow on $HONEY_HOST_NAME"
		},
	]
}
