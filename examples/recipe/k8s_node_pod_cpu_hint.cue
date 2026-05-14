// Remote recipe: on Linux Kubernetes **worker** nodes, sample load, top host PIDs,
// cgroup paths (often contain pod UIDs), and containerd **crictl** stats to hint
// which pods/containers correlate with high CPU. Honey only runs SSH shell; it does
// not call the Kubernetes API.
//
// Prerequisites:
//   - Nodes match your honey search (host: "*" = every row with PrimaryIP).
//   - Typical cluster: **containerd** + **crictl** on the node; `crictl` may require
//     root — this recipe tries `sudo -n crictl` then plain `crictl`. Uses `crictl stats`
//     without `--no-stream` (not supported on some crictl versions). Use
//     recipe.defaults.run_as / per-step run_as if your sudoers allow it.
//   - Cgroup paths differ between cgroup v1/v2 and Kubernetes versions; use output
//     as hints. Map a **pod UID** from `/proc/<pid>/cgroup` to a pod with:
//       kubectl get pods -A -o jsonpath='{range .items[*]}{.metadata.uid} {.metadata.namespace}/{.metadata.name}{"\n"}{end}' | grep <uid>
//   - If `crictl` is missing (custom node images), rely on `ps` + cgroup lines only.
//
// Validate:
//   honey cue-validate examples/recipe/k8s_node_pod_cpu_hint.cue
// Plan:
//   honey cue-exec examples/recipe/k8s_node_pod_cpu_hint.cue "<search>"
// Run:
//   honey cue-exec examples/recipe/k8s_node_pod_cpu_hint.cue "<search>" --execute
// Optional summarizer (last step): set OPENAI_API_KEY for --execute.
recipe: {
	name: "k8s-node-pod-cpu-hint"

	steps: [
		{
			host: "*"
			command: "echo \"=== $HONEY_HOST_NAME ($HONEY_HOST_PRIMARY_IP) load / memory ===\" && (uptime; echo; command -v free >/dev/null 2>&1 && free -h || echo \"skip: no free\") || true"
		},
		{
			host: "*"
			command: "echo \"=== Top processes by CPU% (host PIDs; GNU ps) ===\" && (ps -eo pid,user,%cpu,%mem,rss,etime,args --sort=-%cpu 2>/dev/null | head -16 || echo \"skip: ps --sort not supported\") || true"
		},
		{
			host: "*"
			command: """
echo "=== cgroup snippets for top CPU PIDs (map kubepods… pod UID offline with kubectl) ==="
_pids=$(ps -eo pid= --sort=-%cpu 2>/dev/null | tr -d " " | head -n 8 | tr "\\n" " ")
for _p in $_pids; do
  [ -z "$_p" ] && continue
  if [ -r "/proc/$_p/cgroup" ]; then
    echo "--- pid $_p ---"
    head -n 8 "/proc/$_p/cgroup" 2>/dev/null || true
  fi
done
echo "(done)"
"""
		},
		{
			host: "*"
			run_as: "root"
			command: "echo \"=== crictl container stats (containerd; may skip without permissions) ===\" && if command -v crictl >/dev/null 2>&1; then (sudo -n crictl stats -o table 2>/dev/null || crictl stats -o table 2>/dev/null || echo \"skip: crictl stats failed (try run_as root or fix PATH)\") | head -n 48; else echo \"skip: crictl not in PATH\"; fi || true"
		},
		{
			host: "*"
			run_as: "root"
			command: "echo \"=== crictl pods (first lines) ===\" && if command -v crictl >/dev/null 2>&1; then (sudo -n crictl pods 2>/dev/null || crictl pods 2>/dev/null || echo \"skip: crictl pods failed\") | head -n 20; else echo \"skip: crictl not in PATH\"; fi || true"
		},
		{
			host: "_"
			// notify: {}
			// notify: { services: { http: {} } }
			ai: {
				model: "models/gemini-3.1-pro-preview"
				prompt: """
From the per-host transcripts, summarize in short bullets: load/memory picture,
whether crictl ran, and which pod names or cgroup UIDs look hottest. Say if data
was missing or skipped. Do not invent pod names not present in the text.
"""
			}
		},
	]
}
