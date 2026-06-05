// Idempotent helm upgrade-or-install with inline values.
//
// Runs "helm upgrade --install" on the operator machine via the helm plugin.
// The host field selects which k8s host records trigger this step (for
// sequencing in graph recipes); helm itself uses KUBECONFIG / --kube-context.
//
// Validate:
//   honey cue-validate examples/recipe/helm_upgrade_install.cue
// Plan:
//   honey cue-exec examples/recipe/helm_upgrade_install.cue "re:provider==k8s"
// Execute:
//   honey cue-exec --execute examples/recipe/helm_upgrade_install.cue "re:provider==k8s"
recipe: {
	name: "helm-upgrade-install"
	steps: [
		{
			host: "re:provider==k8s"
			plugin: {
				id:     "helm"
				action: "upgrade_install"
				config: {
					release:   "myapp"
					chart:     "oci://ghcr.io/example/charts/myapp"
					namespace: "production"
					version:   "1.4.2"
					wait:      true
					timeout:   "5m"
					atomic:    true
					values: {
						replicaCount: 3
						image: {tag: "v1.4.2"}
						service: {port: 8080}
					}
				}
			}
		},
	]
}
