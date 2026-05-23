// Operator-side SSH local forward to a service on remote loopback (Redis, HTTP API, etc.).
//
// The tunnel listens on 127.0.0.1 on the machine running honey — not on the remote host.
// Step stdout is JSON: {"host":"127.0.0.1","port":<n>,"mode":"local","remote_host":"...","remote_port":...}
//
// Plan:
//   honey cue-validate examples/recipe/tunnel_local_forward.cue
//   honey cue-exec examples/recipe/tunnel_local_forward.cue "cache-*"
//
// Open tunnel (connect from the operator while cue-exec is running):
//   honey cue-exec --execute examples/recipe/tunnel_local_forward.cue "cache-*"
//   # In another terminal on the same machine:
//   redis-cli -h 127.0.0.1 -p <port from step output>
//
// The optional hold step keeps the run (and tunnel) open for manual debugging.
// Postgres + tunnel_step: see postgres_tunnel_demo.cue
// Docs: website/docs/cue-recipes.md#tunnel-steps
recipe: {
	name: "tunnel-local-forward"

	steps: [
		{
			host: "cache-*"
			tunnel: {
				remote_host: "localhost"
				remote_port: 6379
				// local_port: 16379  // optional fixed operator port (0 = auto)
			}
		},
		{
			host:    "cache-*"
			command: "sleep 300"
		},
	]
}
