// Example: scratch key/value between commands on the same host run (kv_tunnel).
//
// With kv_tunnel: true, each target gets HONEY_KV_URL and HONEY_KV_TOKEN on loopback
// (SSH: reverse-forward to operator stepkv; Kubernetes pod: in-pod Python server — needs python3).
//
// Validate:
//   honey cue-validate examples/recipe/kv_tunnel_example.cue
// Plan:
//   honey cue-exec examples/recipe/kv_tunnel_example.cue "<search>"
// Run (needs curl on the remote for this example):
//   honey cue-exec examples/recipe/kv_tunnel_example.cue "<search>" --execute
//
// Multi-step (shared KV across steps): see kv_tunnel_multistep_example.cue
//
recipe: {
	name: "kv-tunnel-example"

	defaults: { kv_tunnel: true } 

	steps: [
		{
			host:      "*"
			// kv_tunnel: true
			// Store a short value, read it back (plain text body; auth required on every call).
			command: "set -e; curl -fsS -o /dev/null -H \"Authorization: Bearer ${HONEY_KV_TOKEN}\" \"${HONEY_KV_URL}/v1/kv/__health\"; curl -fsS -X PUT -H \"Authorization: Bearer ${HONEY_KV_TOKEN}\" -H 'Content-Type: text/plain; charset=utf-8' --data-binary \"hello-from-$(hostname)\" \"${HONEY_KV_URL}/v1/kv/demo\"; echo \"kv demo key: $(curl -fsS -H \"Authorization: Bearer ${HONEY_KV_TOKEN}\" \"${HONEY_KV_URL}/v1/kv/demo\")\""
		},
	]
}
