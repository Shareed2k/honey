// Real life: query an internal DNS server (CoreDNS / corporate resolver) over UDP.
//
// Scenario: Cluster or datacenter DNS (10.96.0.10, 169.254.20.10, or dc-dns.internal)
// accepts queries only from inside the network. You SSH to a worker/jump host that can
// reach that resolver. OpenSSH -L is TCP-only, so honey uses UDP listen on the operator
// and relays via remote socat (remote_socat: true).
//
// Prerequisites on the SSH target: `socat` installed (apt install socat / yum install socat).
//
// Plan:
//   honey cue-exec examples/recipe/tunnel_udp_dns.cue "k8s-worker-*"
// Execute:
//   honey cue-exec --execute examples/recipe/tunnel_udp_dns.cue "k8s-worker-*"
//
// From the operator (while cue-exec is running):
//   dig @127.0.0.1 -p 1053 kubernetes.default.svc.cluster.local
//   dig @127.0.0.1 -p 1053 +short myapp.internal TXT
//
// Adjust remote_host / remote_port to your resolver (examples below).
//
// Pod vs node: k8s port-forward is TCP-only (see tunnel_k8s_dns_tcp.cue for CoreDNS pod + dig +tcp).
// This UDP recipe requires SSH to a node/worker that can reach the resolver, not host: "k8s:…".
recipe: {
	name: "tunnel-udp-dns"

	steps: [
		{
			host: "k8s-worker-*"
			tunnel: {
				mode:         "udp"
				bind:         "127.0.0.1"
				local_port:   1053
				remote_host:  "10.96.0.10" // kube-dns ClusterIP on many clusters; use dc-dns.internal etc.
				remote_port:  53
				remote_socat: true
			}
		},
		{
			host:    "k8s-worker-*"
			command: "sleep 600"
		},
	]
}
