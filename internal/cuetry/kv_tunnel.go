package cuetry

// KVTunnelEnabled reports whether the step should enable the KV HTTP API on the remote
// (HONEY_KV_URL, HONEY_KV_TOKEN) for command and script steps. For cue-exec over SSH, honey uses one
// operator-side stepkv session for the entire run; kubernetes pod targets use an in-pod server per exec.
func KVTunnelEnabled(s RecipeStep, d *RecipeDefaults) bool {
	if s.KVTunnel != nil {
		return *s.KVTunnel
	}
	if d != nil && d.KVTunnel != nil {
		return *d.KVTunnel
	}
	return false
}
