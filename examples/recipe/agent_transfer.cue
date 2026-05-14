// Agent transfer (source host → object storage → destination host), same flow as the web “A→cloud→B” action.
//
//   honey cue-validate examples/recipe/agent_transfer.cue
//   honey cue-exec examples/recipe/agent_transfer.cue my-filter
//   honey cue-exec --execute --config /path/to/honey.yaml examples/recipe/agent_transfer.cue
//
// Rules:
// - Top-level `host` selects the SOURCE; `agent_transfer.dest_host` selects the DESTINATION.
// - With a non-empty search result set, each selector must match exactly one connectable row.
// - `cloud.provider` / `cloud.bucket` are required; staging object key is chosen at run time.
// - Optional `cloud_backend_ref` picks AWS/GCP signing hints from honey YAML — requires `--config`
//   (CLI/TUI) or server config (web), same as the files API.
recipe: {
	name: "agent-transfer-example"
	steps: [
		{
			host: "re:^web-prod-1$"
			agent_transfer: {
				dest_host:   "re:^db-prod-1$"
				source_path: "/tmp/data.bin"
				dest_path:   "/var/lib/app/data.bin"
				cloud: {
					provider: "s3"
					bucket:   "my-staging-bucket"
					prefix:   "honey-transfer/"
					region:   "us-east-1"
				}
				// cloud_backend_ref: { kind: "aws", name: "prod" }
				keep_object:      false
				max_retries:      3
				agent_remote_dir: "/tmp"
			}
		},
	]
}
