package secrets

import (
	"strings"

	"github.com/shareed2k/honey/internal/cuetry/secrets/stackunwrap"
)

func defaultRegistry(opts Options) *stackunwrap.Registry {
	r := stackunwrap.NewRegistry()
	r.Register(stackunwrap.GCPKMS{})
	r.Register(stackunwrap.AWSKMS{})
	r.Register(stackunwrap.VaultTransit{})
	r.Register(stackunwrap.K8s{})
	r.Register(stackunwrap.Keyring{})
	if p := strings.TrimSpace(opts.AgeIdentityFile); p != "" {
		r.Register(stackunwrap.Age{IdentityFile: p})
	}
	return r
}
