// Real life: query an internal DNS server (CoreDNS / corporate resolver) over UDP
// WITHOUT installing socat on the target — the honey SERVER dials the UDP target
// itself (server-vantage), a pure-Go bridge over the upstream/mesh connection.
//
// This is the counterpart to tunnel_udp_dns.cue (which uses remote_socat: true =
// socat-on-target, target-vantage). The difference is the vantage point:
//
//   remote_socat: true  -> UDP originates from the TARGET's network (needs socat
//                          installed on the target).
//   remote_socat: false -> UDP originates from the honey SERVER's network: it
//                          reaches whatever the SERVER can route to — provider
//                          hosts it already SSHes to, plus anything on a
//                          server-side VPN or the honey mesh — with NO dependency
//                          on the target and NO socat anywhere.
//
// REQUIRES the honey (upstream/mesh) backend: remote_socat: false selects the
// server-side Go UDP bridge (/api/v1/ws/udp) only for the honey provider. On a
// plain SSH host the same flag instead does a direct TCP dial to remote_host
// (UDP-over-TCP), which is wrong for real UDP protocols like DNS — use
// remote_socat: true there. Point host: at a honey-backend host.
//
// SECURITY: the server-bridge lets an authorized caller make the honey server
// originate UDP to remote_host:remote_port (SSRF-shaped). It is gated by the
// server's OPA policy under action "udp_relay" (with the target host+port). If
// OPA is enforced, the policy must allow this action/target or the relay is
// denied with an error.
//
// Plan:
//   honey cue-exec examples/recipe/tunnel_udp_dns_server_bridge.cue "mesh-*"
// Execute:
//   honey cue-exec --execute examples/recipe/tunnel_udp_dns_server_bridge.cue "mesh-*"
//
// From the operator (while cue-exec is running):
//   dig @127.0.0.1 -p 1053 kubernetes.default.svc.cluster.local
//   dig @127.0.0.1 -p 1053 +short myapp.internal TXT
//
// Adjust remote_host / remote_port to your resolver.
recipe: {
	name: "tunnel-udp-dns-server-bridge"

	steps: [
		{
			host: "mesh-*" // a host served by the honey (upstream/mesh) backend
			tunnel: {
				mode:         "udp"
				bind:         "127.0.0.1"
				local_port:   1053
				remote_host:  "10.96.0.10" // resolver the honey SERVER can route to
				remote_port:  53
				remote_socat: false // server-side Go UDP bridge (no socat on target)
			}
		},
		{
			host:    "mesh-*"
			command: "sleep 600"
		},
	]
}
