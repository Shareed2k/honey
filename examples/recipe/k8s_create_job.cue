// Create a Kubernetes batch job with env vars and wait for completion.
//
// Useful for one-off database migrations, seed scripts, or maintenance tasks.
// The job is given a TTL so Kubernetes cleans it up automatically.
// Targets k8s host records (provider == k8s).
//
// Validate:
//   honey cue-validate examples/recipe/k8s_create_job.cue
// Plan:
//   honey cue-exec examples/recipe/k8s_create_job.cue "re:provider==k8s"
// Run:
//   honey cue-exec examples/recipe/k8s_create_job.cue "re:provider==k8s" --execute
recipe: {
	name: "k8s-create-job"
	steps: [
		{
			host: "re:provider==k8s"
			k8s: {
				namespace: "production"
				create_job: {
					name:  "db-migrate"
					image: "registry.example.com/api:latest"
					command: ["bundle", "exec", "rake"]
					args: ["db:migrate"]
					env: {
						RAILS_ENV:    "production"
						DATABASE_URL: "postgres://db:5432/app"
					}
					restart_policy: "Never"
					wait:           true
					ttl_seconds:    600
				}
			}
		},
	]
}
