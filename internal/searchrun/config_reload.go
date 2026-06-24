package searchrun

// ConfigReloader is optionally implemented by factories that hold provider-specific runtime state.
// ReconfigureFromConfig is called whenever the honey config is (re)loaded.
type ConfigReloader interface {
	ReconfigureFromConfig()
}

// ReconfigureFromConfig propagates config to all registered provider factories.
func (r *Registry) ReconfigureFromConfig() {
	for _, f := range r.Factories {
		if reloader, ok := f.(ConfigReloader); ok {
			reloader.ReconfigureFromConfig()
		}
	}
}
