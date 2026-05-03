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

// BuildProviders returns backends from the config file when it defines at least
// one backend entry; otherwise the default quartet from f (each provider once).
func BuildProviders(cfg *config.File, f ProviderFlags) []hosts.Backend {
	if cfg != nil && cfg.HasAnyBackend() {
		n := len(cfg.Backends.GCP) + len(cfg.Backends.AWS) + len(cfg.Backends.Kubernetes) + len(cfg.Backends.Consul) + len(cfg.Backends.Proxmox)
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
			img := e.DebugImage
			if img == "" {
				img = f.K8sDebugImage
			}
			out = append(out, &k8sprovider.K8s{
				Name:           e.Name,
				KubeconfigPath: kpath,
				Context:        ctx,
				Mode:           mode,
				DebugImage:     img,
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
		for _, e := range cfg.Backends.Proxmox {
			url := e.URL
			if url == "" {
				url = f.ProxmoxURL
			}
			user := e.User
			if user == "" {
				user = f.ProxmoxUser
			}
			pass := e.Password
			if pass == "" {
				pass = f.ProxmoxPassword
			}
			tid := e.TokenID
			if tid == "" {
				tid = f.ProxmoxTokenID
			}
			tsec := e.TokenSecret
			if tsec == "" {
				tsec = f.ProxmoxTokenSecret
			}
			insecure := e.Insecure
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
	}
	mode := f.K8sMode
	if mode == "" {
		mode = "nodes"
	}
	return []hosts.Backend{
		&gcp.GCP{Project: f.GCPProject, Zone: f.GCPZone},
		&awsprovider.AWS{Profile: f.AWSProfile, Region: f.AWSRegion},
		&k8sprovider.K8s{KubeconfigPath: f.Kubeconfig, Context: f.KubeContext, Mode: mode, DebugImage: f.K8sDebugImage},
		&consulprovider.Consul{},
		&proxmoxprovider.Proxmox{
			URL:         f.ProxmoxURL,
			User:        f.ProxmoxUser,
			Password:    f.ProxmoxPassword,
			TokenID:     f.ProxmoxTokenID,
			TokenSecret: f.ProxmoxTokenSecret,
			Insecure:    f.ProxmoxInsecure,
		},
	}
}
