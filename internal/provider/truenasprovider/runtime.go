package truenasprovider

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/provider/backendruntime"
)

// TrueNASBackendRuntime holds in-memory TrueNAS API credentials.
type TrueNASBackendRuntime struct {
	Name     string
	URL      string
	Username string
	APIKey   string
	Insecure bool
}

var rtReg = backendruntime.New(func(b TrueNASBackendRuntime) string { return b.Name })

func reconfigureTrueNAS() {
	cfg := config.Get()
	if cfg == nil {
		rtReg.Reconfigure(nil)
		return
	}
	items := make([]TrueNASBackendRuntime, 0, len(cfg.Backends.TrueNAS))
	for _, e := range cfg.Backends.TrueNAS {
		items = append(items, TrueNASBackendRuntime{
			Name:     e.Name,
			URL:      e.URL,
			Username: e.Username,
			APIKey:   e.APIKey,
			Insecure: e.Insecure,
		})
	}
	rtReg.Reconfigure(items)
}

// BackendByName returns API runtime config for a named TrueNAS backend (empty name matches first entry).
func BackendByName(name string) (TrueNASBackendRuntime, bool) {
	return rtReg.ByName(name)
}
