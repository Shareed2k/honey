package searchrun

import (
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/awsprovider"
	"github.com/shareed2k/honey/internal/provider/consulprovider"
	"github.com/shareed2k/honey/internal/provider/gcp"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

// BuildProviders returns backends from the config file when it defines at least
// one backend entry; otherwise the default quartet from f (each provider once).
func BuildProviders(cfg *config.File, f ProviderFlags) []hosts.Backend {
	if cfg != nil && cfg.HasAnyBackend() {
		n := len(cfg.Backends.GCP) + len(cfg.Backends.AWS) + len(cfg.Backends.Kubernetes) + len(cfg.Backends.Consul)
		out := make([]hosts.Backend, 0, n)
		for _, e := range cfg.Backends.GCP {
			proj := e.Project
			if proj == "" {
				proj = f.GCPProject
			}
			zone := e.Zone
			if zone == "" {
				zone = f.GCPZone
			}
			out = append(out, &gcp.GCP{Name: e.Name, Project: proj, Zone: zone})
		}
		for _, e := range cfg.Backends.AWS {
			prof := e.Profile
			if prof == "" {
				prof = f.AWSProfile
			}
			reg := e.Region
			if reg == "" {
				reg = f.AWSRegion
			}
			out = append(out, &awsprovider.AWS{Name: e.Name, Profile: prof, Region: reg})
		}
		for _, e := range cfg.Backends.Kubernetes {
			kpath := e.Kubeconfig
			if kpath == "" {
				kpath = f.Kubeconfig
			}
			ctx := e.Context
			if ctx == "" {
				ctx = f.KubeContext
			}
			mode := e.Mode
			if mode == "" {
				mode = f.K8sMode
			}
			out = append(out, &k8sprovider.K8s{
				Name:           e.Name,
				KubeconfigPath: kpath,
				Context:        ctx,
				Mode:           mode,
			})
		}
		for _, e := range cfg.Backends.Consul {
			addr := e.Addr
			if addr == "" {
				addr = f.ConsulAddr
			}
			dc := e.Datacenter
			if dc == "" {
				dc = f.ConsulDatacenter
			}
			tok := e.Token
			if tok == "" {
				tok = f.ConsulToken
			}
			out = append(out, &consulprovider.Consul{
				Name:       e.Name,
				Addr:       addr,
				Datacenter: dc,
				Token:      tok,
			})
		}
		return out
	}
	mode := f.K8sMode
	if mode == "" {
		mode = "nodes"
	}
	return []hosts.Backend{
		&gcp.GCP{Project: f.GCPProject, Zone: f.GCPZone},
		&awsprovider.AWS{Profile: f.AWSProfile, Region: f.AWSRegion},
		&k8sprovider.K8s{KubeconfigPath: f.Kubeconfig, Context: f.KubeContext, Mode: mode},
		&consulprovider.Consul{},
	}
}
