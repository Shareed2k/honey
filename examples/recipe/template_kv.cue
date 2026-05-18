// Graph recipe: remote kv_tunnel writes a key; local template reads it with kvGet.
//
// Template steps always expose kvGet/kvHas (no extra template field). Remote steps need
// kv_tunnel to populate the operator stepkv session. Keys must not contain "/".
//
// When write_kv runs on many hosts in parallel, they share the key deploy_status (last write wins).
//
// Validate:
//   honey cue-validate examples/recipe/template_kv.cue
recipe: {
	name: "template-kv"
	type: "graph"
	steps: [
		{
			id:        "write_kv"
			host:      "*"
			kv_tunnel: true
			command: """
				set -e
				curl -fsS -X PUT -H "Authorization: Bearer ${HONEY_KV_TOKEN}" \\
				  -H 'Content-Type: text/plain; charset=utf-8' \\
				  --data-binary "ready" "${HONEY_KV_URL}/v1/kv/deploy_status"
				echo ok
				"""
		},
		{
			id:      "render"
			host:    "_"
			depends: ["write_kv"]
			template: {
				template: "status={{ kvGet \"deploy_status\" | default \"unknown\" }}\n"
				data: {}
				output: "RESULT"
			}
		},
		{
			id:      "show"
			host:    "*"
			depends: ["render"]
			env_from: [{
				from_output: "RESULT"
				map: BODY: "stdout"
			}]
			command: "echo \"$BODY\""
		},
	]
}
