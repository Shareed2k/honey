package searchrun

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/awsprovider"
	"github.com/shareed2k/honey/internal/provider/consulprovider"
	"github.com/shareed2k/honey/internal/provider/gcp"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
)

type providerFactory struct {
	FromConfig func(cfg *config.File, f ProviderFlags) []hosts.Backend
	Default    func(f ProviderFlags) hosts.Backend
}

var factories = []providerFactory{
	{
		FromConfig: func(cfg *config.File, f ProviderFlags) []hosts.Backend {
			var out []hosts.Backend
			for _, e := range cfg.Backends.GCP {
				proj, zone := e.Project, e.Zone
				if proj == "" {
					proj = f.GCPProject
				}
				if zone == "" {
					zone = f.GCPZone
				}
				out = append(out, &gcp.GCP{Name: e.Name, Project: proj, Zone: zone})
			}
			return out
		},
		Default: func(f ProviderFlags) hosts.Backend {
			return &gcp.GCP{Project: f.GCPProject, Zone: f.GCPZone}
		},
	},
	{
		FromConfig: func(cfg *config.File, f ProviderFlags) []hosts.Backend {
			var out []hosts.Backend
			for _, e := range cfg.Backends.AWS {
				prof, reg := e.Profile, e.Region
				if prof == "" {
					prof = f.AWSProfile
				}
				if reg == "" {
					reg = f.AWSRegion
				}
				out = append(out, &awsprovider.AWS{Name: e.Name, Profile: prof, Region: reg})
			}
			return out
		},
		Default: func(f ProviderFlags) hosts.Backend {
			return &awsprovider.AWS{Profile: f.AWSProfile, Region: f.AWSRegion}
		},
	},
	{
		FromConfig: func(cfg *config.File, f ProviderFlags) []hosts.Backend {
			var out []hosts.Backend
			for _, e := range cfg.Backends.Kubernetes {
				kpath, ctx, mode, img := e.Kubeconfig, e.Context, e.Mode, e.DebugImage
				if kpath == "" {
					kpath = f.Kubeconfig
				}
				if ctx == "" {
					ctx = f.KubeContext
				}
				if mode == "" {
					mode = f.K8sMode
				}
				if img == "" {
					img = f.K8sDebugImage
				}
				out = append(out, &k8sprovider.K8s{Name: e.Name, KubeconfigPath: kpath, Context: ctx, Mode: mode, DebugImage: img})
			}
			return out
		},
		Default: func(f ProviderFlags) hosts.Backend {
			mode := f.K8sMode
			if mode == "" {
				mode = "nodes"
			}
			return &k8sprovider.K8s{KubeconfigPath: f.Kubeconfig, Context: f.KubeContext, Mode: mode, DebugImage: f.K8sDebugImage}
		},
	},
	{
		FromConfig: func(cfg *config.File, f ProviderFlags) []hosts.Backend {
			var out []hosts.Backend
			for _, e := range cfg.Backends.Consul {
				addr, dc, tok := e.Addr, e.Datacenter, e.Token
				if addr == "" {
					addr = f.ConsulAddr
				}
				if dc == "" {
					dc = f.ConsulDatacenter
				}
				if tok == "" {
					tok = f.ConsulToken
				}
				out = append(out, &consulprovider.Consul{Name: e.Name, Addr: addr, Datacenter: dc, Token: tok})
			}
			return out
		},
		Default: func(_ ProviderFlags) hosts.Backend {
			return &consulprovider.Consul{}
		},
	},
	{
		FromConfig: func(cfg *config.File, f ProviderFlags) []hosts.Backend {
			var out []hosts.Backend
			for _, e := range cfg.Backends.Proxmox {
				url, user, pass, tid, tsec, insecure := e.URL, e.User, e.Password, e.TokenID, e.TokenSecret, e.Insecure
				if url == "" {
					url = f.ProxmoxURL
				}
				if user == "" {
					user = f.ProxmoxUser
				}
				if pass == "" {
					pass = f.ProxmoxPassword
				}
				if tid == "" {
					tid = f.ProxmoxTokenID
				}
				if tsec == "" {
					tsec = f.ProxmoxTokenSecret
				}
				if !insecure && f.ProxmoxInsecure {
					insecure = true
				}
				out = append(out, &proxmoxprovider.Proxmox{
					Name:        e.Name,
					URL:         url,
					User:        user,
					Password:    pass,
					TokenID:     tid,
					TokenSecret: tsec,
					Insecure:    insecure,
				})
			}
			return out
		},
		Default: func(f ProviderFlags) hosts.Backend {
			return &proxmoxprovider.Proxmox{
				URL:         f.ProxmoxURL,
				User:        f.ProxmoxUser,
				Password:    f.ProxmoxPassword,
				TokenID:     f.ProxmoxTokenID,
				TokenSecret: f.ProxmoxTokenSecret,
				Insecure:    f.ProxmoxInsecure,
			}
		},
	},
}

// BuildProviders returns backends from the config file when it defines at least
// one backend entry; otherwise the default quartet from f (each provider once).
func BuildProviders(cfg *config.File, f ProviderFlags) []hosts.Backend {
	if cfg != nil && cfg.HasAnyBackend() {
		// rough estimate based on 1 entry per backend
		out := make([]hosts.Backend, 0, len(factories))
		for _, factory := range factories {
			out = append(out, factory.FromConfig(cfg, f)...)
		}
		return out
	}
	
	out := make([]hosts.Backend, 0, len(factories))
	for _, factory := range factories {
		out = append(out, factory.Default(f))
	}
	return out
}
