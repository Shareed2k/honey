package plugins

import "fmt"

const (
	maxPluginTimeoutMS = 86_400_000 // 24h
	maxPluginMemoryMB  = 4096       // 4 GiB WASM cap
	wasmPageSize       = 65536
)

// extismTimeoutMS converts a validated plugin timeout (milliseconds) for extism.Manifest.
func extismTimeoutMS(ms int) (uint64, error) {
	if ms <= 0 {
		return 0, fmt.Errorf("plugins: timeout_ms must be positive")
	}
	if ms > maxPluginTimeoutMS {
		ms = maxPluginTimeoutMS
	}
	// ms is clamped to [1, maxPluginTimeoutMS]; fits uint64 on all platforms.
	return uint64(ms), nil //nolint:gosec // G115 — bounded non-negative int
}

// extismMemoryPages converts max_memory_mb to Extism WASM pages (64 KiB each).
func extismMemoryPages(maxMB int) (uint32, error) {
	if maxMB <= 0 {
		return 0, fmt.Errorf("plugins: max_memory_mb must be positive")
	}
	if maxMB > maxPluginMemoryMB {
		maxMB = maxPluginMemoryMB
	}
	bytes := int64(maxMB) * 1024 * 1024
	pages := bytes / wasmPageSize
	if pages < 1 {
		pages = 1
	}
	const maxPages = int64(^uint32(0))
	if pages > maxPages {
		return 0, fmt.Errorf("plugins: max_memory_mb too large")
	}
	return uint32(pages), nil //nolint:gosec // G115 — pages derived from clamped maxMB via int64 math
}
