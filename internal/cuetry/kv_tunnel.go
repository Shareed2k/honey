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

// RecipeHasKVTunnel reports whether any step in the recipe uses kv_tunnel.
func RecipeHasKVTunnel(r Recipe) bool {
	for _, s := range r.Steps {
		if KVTunnelEnabled(s, r.Defaults) {
			return true
		}
	}
	return false
}

// KVTunnelEnabled reports whether the step should enable the KV HTTP API on the remote
// (HONEY_KV_URL, HONEY_KV_TOKEN) for command and script steps.
func KVTunnelEnabled(s RecipeStep, d *RecipeDefaults) bool {
	if s.KVTunnel != nil {
		return *s.KVTunnel
	}
	if d != nil && d.KVTunnel != nil {
		return *d.KVTunnel
	}
	return false
}
