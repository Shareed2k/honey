// Demo recipe for the gcloud Docker plugin.
//
//   honey cue-exec --execute examples/recipe/gcloud_plugin_demo.cue <search-filter>
//
// Requires plugins.enabled: true and the gcloud plugin installed.
recipe: {
	name: "gcloud-plugin-demo"
	steps: [
		{
			host: "localhost"
			name: "list-compute-instances"
			plugin: {
				id:     "gcloud"
				action: "compute_list"
				config: {
					project: "my-gcp-project"
					zone:    "us-central1-a"
				}
			}
			register: "gcp_data"
		},
		{
			host: "localhost"
			name: "list-storage-buckets"
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
