// PgHero-style Postgres diagnostics — LOCAL, over the unix-socket tunnel, with
// `peer` auth (no password). Combines mode:"unix" (forward the socket for
// external tools) with on-box pghero-style checks run as the postgres user.
//
// ── "as_user" / postgres identity ────────────────────────────────────────────
// Postgres `peer` maps the OS uid of the process on its unix socket to a role.
// There is NO per-recipe ssh-user field — peer sees the SSH LOGIN user. Two ways
// to be "postgres":
//   • the tunnel step  → the SSH login user must be postgres: pass
//     `--ssh-user postgres` (sshd connects to the remote socket as that user).
//   • a command step   → `run_as: "postgres"` (sudo -n) runs psql on the box as
//     the postgres user → its own local socket → peer. Set once as a default:
//         defaults: { run_as: "postgres" }
//     (run_as does NOT apply to the tunnel step — the tunnel executor ignores
//      it — so mixing the two in one recipe is fine.)
//
// ── Run it (locally) ─────────────────────────────────────────────────────────
//   honey cue-exec --execute --ssh-user postgres \
//     examples/recipe/pghero_local_unix_peer.cue "localhost"
//
// The pg_sock step prints its operator-side socket, e.g.
//   {"mode":"unix","socket":"/tmp/honey-pgsock-XXXX/.s.PGSQL.5432"}
// so an external client can use it (peer, no password) while the run holds:
//   psql -h /tmp/honey-pgsock-XXXX -U postgres      # -h is the DIR, not the file
//
// ── Local prerequisites ──────────────────────────────────────────────────────
//   • SSH server on 127.0.0.1 (macOS: System Settings → General → Sharing →
//     Remote Login) reachable as the postgres OS user (its authorized_keys),
//     with sshd AllowStreamLocalForwarding on (OpenSSH default).
//   • A local Postgres with `peer` in pg_hba.conf for the postgres role and a
//     socket at /var/run/postgresql/.s.PGSQL.5432 (Debian/Ubuntu default; on
//     macOS Homebrew it is typically /tmp/.s.PGSQL.5432 — adjust remote_socket).
//   • The `localhost` host must resolve to a record with PrimaryIP 127.0.0.1.
//
// For the FULL PgHero tool (docker plugin over a TCP tunnel + DSN password),
// see pghero_tunnel_demo.cue. For the plain unix tunnel, postgres_peer_unix_tunnel.cue.
recipe: {
	name: "pghero-local-unix-peer"
	type: "graph"

	// Peer identity for on-box command steps (psql runs as the postgres user).
	// The tunnel step's peer identity still comes from --ssh-user postgres.
	defaults: {
		run_as: "postgres"
	}

	steps: [
		// 1. Forward Postgres's unix socket to a local operator-side socket over
		//    OpenSSH direct-streamlocal. Its stdout carries the local socket path
		//    for any external psql/pgx client (peer, no password).
		{
			id:   "pg_sock"
			host: "*"
			tunnel: {
				mode:          "unix"
				remote_socket: "/var/run/postgresql/.s.PGSQL.5432"
				// local_socket: "/tmp/pgpeer/.s.PGSQL.5432"  // optional fixed path
			}
		},

		// 2. PgHero-style diagnostics, run on the box as the postgres user
		//    (run_as inherited from defaults) → local socket → peer, no password.
		{
			id:      "pghero"
			host:    "*"
			depends: ["pg_sock"]
			command: """
				echo '== active connections =='
				psql -U postgres -d postgres -tAqc "select count(*) from pg_stat_activity where state = 'active'"

				echo '== least-used indexes (idx_scan asc, top 5) =='
				psql -U postgres -d postgres -tAqc "select schemaname||'.'||relname||' / '||indexrelname||' = '||idx_scan from pg_stat_user_indexes order by idx_scan asc limit 5"

				echo '== largest tables (top 5) =='
				psql -U postgres -d postgres -tAqc "select relname||' = '||pg_size_pretty(pg_total_relation_size(relid)) from pg_stat_user_tables order by pg_total_relation_size(relid) desc limit 5"

				echo '== longest-running queries (top 5) =='
				psql -U postgres -d postgres -tAqc "select pid||' '||state||' '||coalesce(round(extract(epoch from now()-query_start))::text,'-')||'s' from pg_stat_activity where state <> 'idle' order by query_start asc limit 5"
				"""
		},
	]
}
