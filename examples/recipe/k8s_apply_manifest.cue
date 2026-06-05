// Apply a Kubernetes YAML manifest inline using server-side apply.
//
// Suitable for ConfigMaps, Deployments, Services, or any resource whose
// manifest can be inlined as a CUE string literal. server_side: true enables
// field manager ownership tracking (recommended for GitOps-style updates).
// Targets k8s host records (provider == k8s).
//
// Validate:
//   honey cue-validate examples/recipe/k8s_apply_manifest.cue
// Plan:
//   honey cue-exec examples/recipe/k8s_apply_manifest.cue "re:provider==k8s"
// Run:
//   honey cue-exec examples/recipe/k8s_apply_manifest.cue "re:provider==k8s" --execute
recipe: {
	name: "k8s-apply-manifest"
	steps: [
		{
			host: "re:provider==k8s"
			k8s: {
				namespace: "production"
				apply: {
					server_side: true
					manifest: """
						apiVersion: v1
						kind: ConfigMap
						metadata:
						  name: api-config
						  namespace: production
						data:
						  LOG_LEVEL: info
						  FEATURE_FLAGS: "rollout=true,new_ui=false"
						"""
				}
			}
		},
	]
}
