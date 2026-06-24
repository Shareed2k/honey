package truenasprovider

import (
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/config"
)

// TrueNASBackendRuntime holds in-memory TrueNAS API credentials.
type TrueNASBackendRuntime struct {
	Name     string
	URL      string
	Username string
	APIKey   string
	Insecure bool
}

var (
	rtMu        sync.RWMutex
	truenasBack []TrueNASBackendRuntime
)

func reconfigureTrueNAS() {
	cfg := config.Get()
	rtMu.Lock()
	defer rtMu.Unlock()
	truenasBack = truenasBack[:0]
	if cfg == nil {
		return
	}
	for _, e := range cfg.Backends.TrueNAS {
		truenasBack = append(truenasBack, TrueNASBackendRuntime{
			Name:     e.Name,
			URL:      e.URL,
			Username: e.Username,
			APIKey:   e.APIKey,
			Insecure: e.Insecure,
		})
	}
}

// BackendByName returns API runtime config for a named TrueNAS backend (empty name matches first entry).
func BackendByName(name string) (TrueNASBackendRuntime, bool) {
	rtMu.RLock()
	defer rtMu.RUnlock()
	name = strings.TrimSpace(name)
	if len(truenasBack) == 0 {
		return TrueNASBackendRuntime{}, false
	}
	if name == "" {
		return truenasBack[0], true
	}
	for _, b := range truenasBack {
		if b.Name == name {
			return b, true
		}
	}
	return TrueNASBackendRuntime{}, false
}
