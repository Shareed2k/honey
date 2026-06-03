// Example recipe using the docker step to build and push an image.
//
// Validate:
//   honey cue-validate examples/recipe/docker_module_demo.cue
// Plan:
//   honey cue-exec examples/recipe/docker_module_demo.cue "my-docker-host"
// Run:
//   honey cue-exec examples/recipe/docker_module_demo.cue "my-docker-host" --execute
recipe: {
	name: "docker-module-demo"
	type: "graph"
	steps: [
		{
			id: "build-image"
			host: "my-docker-host"
			docker: {
				action: "build"
				output: "build_result"
				build: {
					context:    "./app"
					dockerfile: "./app/Dockerfile"
					tags: [
						"my-app:latest",
						"my-registry/my-app:1.0.0",
					]
				}
			}
		},
		{
			id: "push-image"
			host: "my-docker-host"
			depends: ["build-image"]
			docker: {
				action: "push"
				push: {
					image: "my-registry/my-app:1.0.0"
				}
			}
		},
	]
}
