// Graph recipe: when compares declared recipe secrets (dry-run uses redacted placeholders).
recipe: {
	name: "graph-when-secrets"
	type: "graph"
	defaults: {
		secrets: {FLAG: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}
	}
	steps: [
		{
			id:      "check"
			host:    "*"
			when:    "secrets['FLAG'] != ''"
			command: "echo flag present on $HONEY_HOST_NAME"
		},
	]
}
