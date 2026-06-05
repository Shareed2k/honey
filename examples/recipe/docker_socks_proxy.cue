// Standalone SOCKS5 Bastion Tunnel on a Docker Host (Dynamic forwarding).
// Spins up a local SOCKS5 proxy on port 1080 of the operator machine,
// routing all local application traffic through the remote Docker host.
//
// Plan:
//   honey cue-exec examples/recipe/docker_socks_proxy.cue "my-docker-bastion-host"
// Execute:
//   honey cue-exec --execute examples/recipe/docker_socks_proxy.cue "my-docker-bastion-host"
//
// Operator query:
//   curl --socks5-hostname localhost:1080 http://internal-only-api.local/v1/status

recipe: {
	name: "docker-socks-proxy"

	steps: [
		{
			host: "my-docker-bastion-host"
			tunnel: {
				mode:       "dynamic"
				local_port: 1080
			}
		},
		{
			host:    "my-docker-bastion-host"
			command: "sleep 300"
		}
	]
}
