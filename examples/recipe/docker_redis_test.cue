// Example recipe: Start Redis, interact with it, and shut it down.
//
// Validate:
//   honey cue-validate examples/recipe/docker_redis_test.cue
// Run:
//   honey cue-exec examples/recipe/docker_redis_test.cue "local" --execute

recipe: {
	name: "docker-redis-test"
	type: "graph"
	defaults: { run_as: "root" }
	
	steps: [
		{
			id: "start-redis"
			host: "_"
			docker: {
				socket: "unix:///Users/shareed2k/.colima/default/docker.sock"
				action: "run"
				run: {
					image: "redis:alpine"
					name:  "honey-test-redis"
					detach: true
				}
			}
		},
		{
			id: "ping-redis"
			depends: ["start-redis"]
			host: "_"
			docker: {
				socket: "unix:///Users/shareed2k/.colima/default/docker.sock"
				action: "exec"
				output: "redis_output"
				exec: {
					container: "honey-test-redis"
					command: ["sh", "-c", "sleep 1 && redis-cli PING"]
				}
			}
		},
		{
			id: "stop-redis"
			depends: ["ping-redis"]
			host: "_"
			docker: {
				socket: "unix:///Users/shareed2k/.colima/default/docker.sock"
				action: "stop"
				stop: {
					container: "honey-test-redis"
					rm: true
				}
			}
		},
		{
			id: "print-result"
			depends: ["ping-redis"]
			host: "_"
			env_from: [{
				step: "ping-redis"
				map: REDIS_OUT: "stdout"
			}]
			template: {
				template: "Redis responded with: {{ .REDIS_OUT }}\n"
				data: {}
			}
		}
	]
}
