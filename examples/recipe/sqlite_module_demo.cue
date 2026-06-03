// Example recipe using the sqlite WASM module.
//
// SQLite is embedded inside plugin.wasm. The plugin does not call a sqlite3
// binary and does not use host_exec. It can only read database files mounted
// into WASI by plugin.yaml allowed_paths.
//
// Install:
//   task build-plugin-modules
//   mkdir -p ~/.config/honey/plugins/sqlite
//   cp examples/plugins/sqlite/plugin.yaml examples/plugins/sqlite/plugin.wasm ~/.config/honey/plugins/sqlite/
//
// Edit ~/.config/honey/plugins/sqlite/plugin.yaml and add a mount for your DBs:
//   allowed_paths:
//     "/sqlite": "/absolute/host/path/to/sqlite-dbs"
//
// Then run:
//   honey cue-exec examples/recipe/sqlite_module_demo.cue "*"
//   honey cue-exec --execute examples/recipe/sqlite_module_demo.cue "*"
recipe: {
	name: "sqlite-module-demo"

	steps: [
		{
			host: "*"
			plugin: {
				id:     "sqlite"
				action: "query"
				config: {
					dsn:      "file:/sqlite/app.db?mode=ro"
					readonly: true
					sql:      "SELECT sqlite_version() AS version"
					params:   []
				}
			}
		},
	]
}
