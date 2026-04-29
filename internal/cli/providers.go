package cli

import (
	"hostctl/internal/config"
	"hostctl/internal/hosts"
	"hostctl/internal/provider/awsprovider"
	"hostctl/internal/provider/consulprovider"
	"hostctl/internal/provider/gcp"
	"hostctl/internal/provider/k8sprovider"
)

// buildProviders returns backends from the config file when it defines at least
// one backend entry; otherwise the default quartet from CLI flags (each
// provider once). Struct fields from YAML may be left empty and are filled from
// the corresponding CLI flags for convenience.
func buildProviders(cfg *config.File) []hosts.Backend {
	if cfg != nil && cfg.HasAnyBackend() {
		var out []hosts.Backend
		for _, e := range cfg.Backends.GCP {
			proj := e.Project
			if proj == "" {
				proj = flagGCPProject
			}
			zone := e.Zone
			if zone == "" {
				zone = flagGCPZone
			}
			out = append(out, &gcp.GCP{Name: e.Name, Project: proj, Zone: zone})
		}
		for _, e := range cfg.Backends.AWS {
			prof := e.Profile
			if prof == "" {
				prof = flagAWSProfile
			}
			reg := e.Region
			if reg == "" {
				reg = flagAWSRegion
			}
			out = append(out, &awsprovider.AWS{Name: e.Name, Profile: prof, Region: reg})
		}
		for _, e := range cfg.Backends.Kubernetes {
			kpath := e.Kubeconfig
			if kpath == "" {
				kpath = flagKubeconfig
			}
			ctx := e.Context
			if ctx == "" {
				ctx = flagKubeContext
			}
			mode := e.Mode
			if mode == "" {
				mode = flagK8sMode
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
				addr = flagConsulAddr
			}
			dc := e.Datacenter
			if dc == "" {
				dc = flagConsulDC
			}
			tok := e.Token
			if tok == "" {
				tok = flagConsulToken
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
	return []hosts.Backend{
		&gcp.GCP{Project: flagGCPProject, Zone: flagGCPZone},
		&awsprovider.AWS{Profile: flagAWSProfile, Region: flagAWSRegion},
		&k8sprovider.K8s{KubeconfigPath: flagKubeconfig, Context: flagKubeContext, Mode: flagK8sMode},
		&consulprovider.Consul{},
	}
}
