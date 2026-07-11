// Demo recipe for the gcloud Docker plugin.
//
//   honey cue-exec --execute examples/recipe/gcloud_plugin_demo.cue
//
// Requires plugins.enabled: true and the gcloud plugin installed. Steps
// target host: "_" (local-only) — runtime: docker plugins always execute in
// a container on the operator machine and never touch a target host, so no
// search backend or matching host record is needed at all.
recipe: {
	name: "gcloud-plugin-demo"
	steps: [
		{
			host: "_"
			plugin: {
				id:     "gcloud"
				action: "compute_list"
				config: {
					project: "my-gcp-project"
					zone:    "us-central1-a"
				}
			}
		},
		{
			host: "_"
			plugin: {
				id:     "gcloud"
				action: "storage_ls"
				config: {
					url: "gs://my-demo-bucket/"
				}
			}
		}
	]
}
