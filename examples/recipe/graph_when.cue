// Graph recipe: per-host when conditions using prior step stdout.
recipe: {
	name: "graph-when"
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
			when:    "steps['fetch'].stdout.contains('shard')"
			command: "echo deploy on $HONEY_HOST_NAME"
		},
	]
}
