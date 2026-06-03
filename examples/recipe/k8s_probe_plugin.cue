// Example: custom WASM plugin using pluginpdk.K8sHTTP to call k8s API.
//
// The k8s-probe plugin fetches /version from each k8s cluster via the
// k8s_http host function — no kubeconfig or credentials in the plugin.
//
// Requires: k8s-probe plugin built and installed in honey plugins directory.
//   cd examples/plugins/k8s_probe
//   GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o plugin.wasm .
//
// Validate:
//   honey cue-validate examples/recipe/k8s_probe_plugin.cue
// Execute:
//   honey cue-exec --execute examples/recipe/k8s_probe_plugin.cue "re:provider==k8s"
recipe: {
	name: "k8s-probe"
	type: "graph"
	steps: [
		{
			id:   "version"
			host: "re:provider==k8s"
			plugin: {
				id:     "k8s-probe"
				action: "version"
				config: {}
			}
		},
		{
			id:      "health"
			host:    "re:provider==k8s"
			depends: ["version"]
			plugin: {
				id:     "k8s-probe"
				action: "health"
				config: {}
			}
		},
	]
}
