// Example recipe using the docker step to run container.
//
// Validate:
//   honey cue-validate examples/recipe/docker_run.cue
// Plan:
//   honey cue-exec examples/recipe/docker_run.cue "my-docker-host"
// Run:
//   honey cue-exec examples/recipe/docker_run.cue "my-docker-host" --execute
recipe: {
	name: "docker-run"
	type: "graph"
	defaults: {run_as: "root"}
	steps: [
		{
			id: "run-nginx"
			host: "*"
			docker: {
				action: "run"
				output: "run_out"
				run: {
					image: "nginx:stable-alpine"
					name:  "my-web-server"
					ports: ["80:80"]
					volumes: ["/var/www:/usr/share/nginx/html"]
					env: {
						ENV: "production"
					}
					detach: true // Run in background
				}
			}
		},
	]
}
