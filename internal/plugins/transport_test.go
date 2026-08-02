package plugins

import "testing"

// TestPluginTransport_ExtismTransportSatisfiesInterface is a compile-time
// characterization: extismTransport must keep satisfying pluginTransport
// through the refactor below.
func TestPluginTransport_ExtismTransportSatisfiesInterface(_ *testing.T) {
	var _ pluginTransport = (*extismTransport)(nil)
}
