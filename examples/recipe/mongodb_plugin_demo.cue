// Demo recipe for the mongodb Docker plugin.
//
//   honey cue-exec --execute examples/recipe/mongodb_plugin_demo.cue <search-filter>
//
// Requires plugins.enabled: true and the mongodb plugin installed.
recipe: {
	name: "mongodb-plugin-demo"
	defaults: {
		secrets: {
			MONGO_URI: "env://MONGO_URI"
		}
	}
	steps: [
		{
			host: "localhost"
			name: "query-users"
			plugin: {
				id:     "mongodb"
				action: "query"
				config: {
					uri:        "{{ secrets.MONGO_URI }}"
					database:   "usersdb"
					collection: "users"
					query:      "{ status: 'active' }"
				}
			}
			register: "mongo_users"
		},
		{
			host: "localhost"
			name: "eval-script"
			plugin: {
				id:     "mongodb"
				action: "eval"
				config: {
					uri:    "{{ secrets.MONGO_URI }}"
					script: "printjson(db.serverStatus())"
				}
			}
		}
	]
}
