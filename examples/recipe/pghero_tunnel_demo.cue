// PgHero read-only diagnostics against a Postgres reachable only through an SSH
// tunnel (bastion), using the pghero_diagnostics docker plugin.
//
// Shape: step 1 opens a local SSH port-forward via the bastion; step 2 runs the
// docker plugin on the operator machine (host "_") and connects to the tunnel's
// operator-side loopback endpoint through host.docker.internal (Docker Desktop).
//
// Caveats (see docs): host.docker.internal is provided by Docker Desktop /
// Colima only; on Linux the honey docker-plugin container has no host-gateway
// route to the operator's loopback (use variant 2, docker.network: host,
// below). And the recipe tunnel must stay open for the dependent docker step.
//
// Plan:
//   honey cue-exec --config <cfg> examples/recipe/pghero_tunnel_demo.cue "bastion"
// Execute (real):
//   honey cue-exec --config <cfg> --execute -e PG_PASS=secret \
//     examples/recipe/pghero_tunnel_demo.cue "bastion"
//
// Two variants for reaching the tunnel's operator-side loopback endpoint
// (127.0.0.1:15432, opened by the pg_tunnel step below) from inside the
// pghero_diagnostics plugin container:
//
//   1. Docker Desktop / Colima (macOS, Windows) — the default below. The
//      plugin container stays on the default bridge network and reaches the
//      operator loopback via the `host.docker.internal` gateway, which
//      Docker Desktop/Colima provide automatically. No extra config needed.
//
//   2. Linux — `host.docker.internal` does not resolve there and there is no
//      bridge route to the operator's loopback. Instead, run the plugin
//      container with host networking so it shares the operator's network
//      namespace directly:
//        a. pghero_diagnostics' plugin.yaml:
//             docker:
//               network: host
//        b. honey config (honey.yaml):
//             plugins:
//               allow_host_network: true   # required or the plugin fails to load
//        c. In the `pghero` step's config below, change database_url's host
//           from `host.docker.internal:15432` to `127.0.0.1:15432` — with
//           host networking the container's loopback *is* the operator's
//           loopback, so it reaches the tunnel's local_port directly.
//      See website/docs/plugins-development.md ("Docker runtime plugins") for
//      the full docker.network / allow_host_network reference.

recipe: {
	name: "pghero-tunnel-demo"
	type: "graph"

	steps: [
		{
			id:   "pg_tunnel"
			host: "*"
			tunnel: {
				mode:        "local"
				remote_host: "host.docker.internal" // Postgres reachable from the bastion
				remote_port: 55432
				local_port:  15432
				bind:        "127.0.0.1"
			}
		},
		{
			id:      "pghero"
			host:    "_"
			depends: ["pg_tunnel"]
			plugin: {
				id:     "pghero_diagnostics"
				action: "diagnose"
				config: {
					// Docker Desktop/Colima (default): host.docker.internal.
					// Linux host-net variant: swap the host for 127.0.0.1 (see
					// header comment above) — same port, same database.
					database_url: "postgres://postgres:${PG_PASS}@host.docker.internal:15432/app?sslmode=disable&statement_timeout=10000"
					checks: ["connections", "index_usage", "unused_indexes", "space", "slow_queries"]
				}
			}
		},
	]
}
