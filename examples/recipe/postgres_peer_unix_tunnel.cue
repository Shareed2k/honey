// Postgres `peer` auth over a honey unix-socket tunnel (StreamLocal).
//
// peer maps the OS uid of the process on postgres's unix socket to a role. Over
// SSH that process is sshd running as the SSH LOGIN USER, so you MUST log in as
// the postgres OS user (or a pg_ident-mapped user):
//
//   honey cue-exec --execute --ssh-user postgres \
//     examples/recipe/postgres_peer_unix_tunnel.cue "db-primary"
//
// The tunnel forwards the remote socket to a local socket named .s.PGSQL.5432
// inside a private temp dir; the tunnel step's stdout is JSON carrying it, e.g.
//   {"mode":"unix","socket":"/tmp/honey-pgsock-XXXX/.s.PGSQL.5432"}
// In another terminal, point psql at that DIRECTORY (not the socket file):
//
//   psql -h /tmp/honey-pgsock-XXXX -U postgres -c "select current_user"
//   # -> postgres, no password (peer).
//
// Requirements & caveats:
//   - Remote sshd must allow StreamLocalForwarding (default in OpenSSH).
//   - A plain TCP tunnel (mode:"local" to 127.0.0.1:5432) can NEVER do peer —
//     TCP uses pg_hba `host` rules (md5/scram), not `peer`.
//   - This is the forward primitive only: honey's postgres plugin still dials
//     the operator-side TCP endpoint, so it cannot use peer over this tunnel.
//   - Over the mesh the "unix:<path>" target is OPA-gated (action "tunnel").
//
// See also: postgres_tunnel_demo.cue (TCP), website/docs/cue-recipes.md.
recipe: {
	name: "postgres-peer-unix-tunnel"
	type: "graph"

	steps: [
		{
			id:   "pg_sock"
			host: "db-primary"
			tunnel: {
				mode:          "unix"
				remote_socket: "/var/run/postgresql/.s.PGSQL.5432"
				// local_socket: "/tmp/pgpeer/.s.PGSQL.5432"  // optional fixed path
			}
		},
		{
			id:      "hold"
			host:    "db-primary"
			depends: ["pg_sock"]
			command: "sleep 300"
		},
	]
}
