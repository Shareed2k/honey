// Graph recipe: check release status then rollback to previous revision on failure.
//
// Step 1 captures helm status JSON.
// Step 2 (when status is not "deployed") rolls back to the previous revision.
//
// Validate:
//   honey cue-validate examples/recipe/helm_rollback.cue
// Execute:
//   honey cue-exec --execute examples/recipe/helm_rollback.cue "re:provider==k8s"
recipe: {
	name: "helm-rollback"
	type: "graph"
	steps: [
		{
			id:   "status"
			host: "re:provider==k8s"
			plugin: {
				id:     "helm"
				action: "status"
				config: {
					release:   "myapp"
					namespace: "production"
				}
			}
		},
		{
			id:      "rollback"
			host:    "re:provider==k8s"
			depends: ["status"]
			when:    "!steps['status'].succeeded || steps['status'].stdout.contains('\"status\":\"failed\"')"
			plugin: {
				id:     "helm"
				action: "rollback"
				config: {
					release:   "myapp"
					namespace: "production"
					wait:      true
					timeout:   "5m"
				}
			}
		},
	]
}
