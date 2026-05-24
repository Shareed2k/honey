// Real life: L3 tunnel (ssh -w) to reach a private subnet through a datacenter gateway.
//
// Scenario: Remote site has services on 10.48.0.0/16 (monitoring, iLO, storage mgmt)
// with no direct route from your laptop. A Linux router/gateway in the DC runs sshd and
// has the tun peer configured on its side. Honey starts `ssh -w 0:0` from the operator.
//
// Requirements:
//   - OpenSSH on both sides with PermitTunnel yes (or point-to-point enabled)
//   - Operator: root or CAP_NET_ADMIN to create/configure tun0
//   - Remote admin: matching tun0 address + route (outside this recipe)
//
// Plan:
//   honey cue-exec examples/recipe/tunnel_tun_datacenter.cue "dc-gateway-*"
// Execute:
//   honey cue-exec --execute examples/recipe/tunnel_tun_datacenter.cue "dc-gateway-*"
//
// Step stdout (no TCP port):
//   {"mode":"tun","tun_name":"tun0"}
//
// On the operator AFTER the tunnel step succeeds (manual — not automated by honey):
//   sudo ip link set tun0 up
//   sudo ip addr add 10.255.0.2/30 dev tun0
//   sudo ip route add 10.48.0.0/16 dev tun0
//   ping -c2 10.48.1.1
//   curl http://10.48.1.5:9090/metrics
//
// Remote gateway (example, run once on dc-gateway by your network team):
//   ip addr add 10.255.0.1/30 dev tun0
//   ip route add 10.255.0.0/30 dev tun0
//   # NAT or static routes so 10.48.0.0/16 is reachable via tun0
//
// Hold step keeps ssh -w alive while you configure routes and test.
recipe: {
	name: "tunnel-tun-datacenter"

	steps: [
		{
			host: "dc-gateway-*"
			tunnel: {
				mode:       "tun"
				tun_local:  0
				tun_remote: 0
			}
		},
		{
			host:    "dc-gateway-*"
			command: "sleep 3600"
		},
	]
}
