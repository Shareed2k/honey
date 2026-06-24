package config

import "sync/atomic"

var globalConfig atomic.Pointer[File]

// Set updates the global configuration object.
func Set(cfg *File) {
	globalConfig.Store(cfg)
}

// Get returns the current global configuration object. If none is set, returns an empty config.
func Get() *File {
	cfg := globalConfig.Load()
	if cfg == nil {
		return &File{}
	}
	return cfg
}
