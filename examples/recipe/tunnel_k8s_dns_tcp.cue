// Cluster DNS from a pod via k8s port-forward (TCP only).
//
// Kubernetes port-forward is TCP. CoreDNS listens on TCP/UDP 53; use dig +tcp on the operator.
// You must target a CoreDNS pod (or another pod listening on 53), not the ClusterIP service.
//
// Find a CoreDNS pod:
//   kubectl get pods -n kube-system -l k8s-app=kube-dns -o name
//   honey search "k8s:coredns-…"   # pod must appear in inventory with provider k8s
//
// Plan:
//   honey cue-exec examples/recipe/tunnel_k8s_dns_tcp.cue "k8s:coredns-xxxxx"
// Execute:
//   honey cue-exec --execute examples/recipe/tunnel_k8s_dns_tcp.cue "k8s:coredns-xxxxx"
//
// Operator (TCP DNS — not UDP):
//   dig @127.0.0.1 -p 1053 +tcp kubernetes.default.svc.cluster.local
//
// For UDP DNS from your laptop, use SSH to a node/worker instead:
//   examples/recipe/tunnel_udp_dns.cue
//
// To query DNS inside the cluster without a tunnel, run dig in a debug pod (no tunnel step):
//   host: "k8s:debug-pod"
//   command: "dig +short kubernetes.default.svc.cluster.local"
recipe: {
	name: "tunnel-k8s-dns-tcp"

	steps: [
		{
			host: "k8s:coredns-xxxxx"
			tunnel: {
				remote_port: 53
				local_port:  1053
			}
		},
		{
			host:    "k8s:coredns-xxxxx"
			command: "sleep 600"
		},
	]
}
