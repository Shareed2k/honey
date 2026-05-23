// Real life: browse internal HTTP services through a bastion (SSH dynamic forward / SOCKS5).
//
// Scenario: Grafana, Jenkins, or an internal API (http://grafana.internal:3000) is only
// reachable from hosts inside the VPC. Your laptop has SSH to a bastion; nothing else.
//
// Plan:
//   honey cue-validate examples/recipe/tunnel_socks.cue
//   honey cue-exec examples/recipe/tunnel_socks.cue "bastion-*"
//
// Open SOCKS5 on the operator (127.0.0.1:1080) and keep it up for 30 minutes:
//   honey cue-exec --execute examples/recipe/tunnel_socks.cue "bastion-*"
//
// From another terminal on the SAME machine (operator):
//   # HTTP through the bastion (DNS resolved on the bastion side)
//   curl --socks5-hostname 127.0.0.1:1080 http://grafana.internal:3000/api/health
//
//   # Postgres/MySQL clients that speak SOCKS (or use proxychains)
//   proxychains4 psql 'host=db.internal port=5432 user=ro dbname=app sslmode=require'
//
// Browser (Firefox): Settings → Network → Manual proxy → SOCKS v5 → 127.0.0.1:1080,
// enable "Proxy DNS when using SOCKS v5" so internal hostnames resolve via the bastion.
//
// Requires: outbound SSH from operator to bastion; bastion can reach internal hostnames.
recipe: {
	name: "tunnel-socks-bastion"

	steps: [
		{
			host: "bastion-*"
			tunnel: {
				mode:       "dynamic"
				bind:       "127.0.0.1"
				local_port: 1080
				// share_key: "corp-bastion-socks"  // reuse same listen port in one run / pool
			}
		},
		{
			host:    "bastion-*"
			command: "sleep 1800"
		},
	]
}
