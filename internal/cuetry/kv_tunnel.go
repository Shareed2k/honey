package cuetry

// RecipeHasTemplateStep reports whether any step uses template rendering.
func RecipeHasTemplateStep(r Recipe) bool {
	for _, s := range r.Steps {
		if s.Template != nil {
			return true
		}
	}
	return false
}

// RecipeHasKVTunnel reports whether the recipe run uses stepkv (always true; kv_tunnel is always on).
func RecipeHasKVTunnel(_ Recipe) bool {
	return true
}

// KVTunnelEnabled reports whether the step should enable the KV HTTP API on the remote
// (HONEY_KV_URL, HONEY_KV_TOKEN). Always true; recipe kv_tunnel fields are deprecated no-ops.
func KVTunnelEnabled(_ RecipeStep, _ *RecipeDefaults) bool {
	return true
}
