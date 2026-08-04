// PgHero diagnostics via the docker plugin, reaching a Postgres that listens on
// a UNIX SOCKET only — bridged to a local TCP port with mode:"unix" + local_port.
//
// The chain:
//   pg_sock:  local TCP 127.0.0.1:15432  --(direct-streamlocal over SSH)-->  the
//             remote Postgres unix socket /var/run/postgresql/.s.PGSQL.5432
//   pghero:   the docker plugin (on the operator) dials host.docker.internal:15432
//             --> the tunnel --> the remote socket.
//
// Why local_port (not local_socket): the pghero plugin runs in a container and
// speaks TCP; a local UNIX socket (plain mode:"unix") would be unreachable from
// it. `local_port` switches the operator side to a TCP listener that forwards to
// the remote unix socket over direct-streamlocal — so any TCP client works.
//
// ── Auth ─────────────────────────────────────────────────────────────────────
// The remote Postgres sees a UNIX-SOCKET connection (sshd opens it as the SSH
// login user), so its pg_hba `local` rules apply — NOT `host`:
//   • `local … scram-sha-256`/`md5` → put the password in DATABASE_URL (below).
//   • `local … peer` + --ssh-user postgres → peer maps the postgres OS user;
//     the DSN password is then ignored (any value works).
//
// ── Run ──────────────────────────────────────────────────────────────────────
//   honey cue-exec --config <cfg> --execute -e PG_PASS=secret \
//     examples/recipe/pghero_unix_tunnel.cue "<remote-with-postgres>"
//   # peer variant: add --ssh-user postgres
//
// ── Prereqs ──────────────────────────────────────────────────────────────────
//   • Build + install the plugin image (see pghero_tunnel_demo.cue / plugins-development.md).
//   • Remote sshd with AllowStreamLocalForwarding on (OpenSSH default).
//   • Docker Desktop / Colima for host.docker.internal (Linux: docker.network:host
//     + 127.0.0.1:15432 — see pghero_tunnel_demo.cue).
//
// Plain-TCP Postgres (already listening on :5432)? Use pghero_tunnel_demo.cue
// (mode:"local"). Unix socket + your own psql (peer)? postgres_peer_unix_tunnel.cue.
recipe: {
	name: "pghero-unix-tunnel"
	type: "graph"

	steps: [
		{
			id:   "pg_sock"
			host: "*"
			tunnel: {
				mode:          "unix"
				remote_socket: "/var/run/postgresql/.s.PGSQL.5432"
				local_port:    15432 // TCP listener → remote unix socket (direct-streamlocal)
				bind:          "127.0.0.1"
			}
		},
		{
			id:      "pghero"
			host:    "_"
			depends: ["pg_sock"]
			plugin: {
				id:     "pghero_diagnostics"
				action: "diagnose"
				config: {
					// Docker Desktop/Colima: host.docker.internal. Linux host-net
					// variant: 127.0.0.1:15432.
					database_url: "postgres://postgres:${PG_PASS}@host.docker.internal:15432/postgres?sslmode=disable&statement_timeout=10000"
					checks: ["connections", "index_usage", "unused_indexes", "space", "slow_queries"]
				}
			}
		},
	]
}
