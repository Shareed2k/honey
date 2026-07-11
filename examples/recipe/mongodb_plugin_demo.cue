// Demo recipe for the mongodb Docker plugin.
//
//   honey cue-exec --execute examples/recipe/mongodb_plugin_demo.cue
//
// Requires plugins.enabled: true and the mongodb plugin installed. Steps
// target host: "_" (local-only) — runtime: docker plugins always execute in
// a container on the operator machine and never touch a target host, so no
// search backend or matching host record is needed at all.
recipe: {
	name: "mongodb-plugin-demo"
	steps: [
		{
			host: "_"
			plugin: {
				id:     "mongodb"
				action: "query"
				config: {
					// Replace with your real connection string, or seal one
					// with `honey secrets seal` and reference it via
					// recipe/step `secrets:` (must be secure:v1:... — there
					// is no env:-ref convenience scheme for recipe secrets).
					uri:        "mongodb://localhost:27017"
					database:   "usersdb"
					collection: "users"
					query:      "{ status: 'active' }"
				}
			}
		},
		{
			host: "_"
			plugin: {
				id:     "mongodb"
				action: "eval"
				config: {
					uri:    "mongodb://localhost:27017"
					script: "printjson(db.serverStatus())"
				}
			}
		}
	]
}
