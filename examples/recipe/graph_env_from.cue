// Graph recipe: capture stdout from a dependency step into env vars (per host).
recipe: {
	name: "graph-env-from"
	type: "graph"
	steps: [
		{
			id:      "fetch"
			host:    "*"
			command: "echo shard-$(hostname -s)"
		},
		{
			id:      "deploy"
			host:    "*"
			depends: ["fetch"]
			env_from: [{
				step: "fetch"
				map: SHARD_TAG: "stdout"
			}]
			command: "echo deploying with SHARD_TAG=$SHARD_TAG"
		},
	]
}
