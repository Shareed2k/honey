package config

import "testing"

func TestFileSanitize(t *testing.T) {
	f := File{
		Defaults: Defaults{
			SSHUser:         "  ops  ",
			CacheTTL:        " 10m ",
			RecordDir:       " /tmp/recs ",
			SecretsProvider: "  gcpkms://  ",
		},
		Plugins: Plugins{Directory: "  /plugins  "},
		Backends: Backends{
			Local: []LocalBackend{{
				Name: "  local-a  ",
				Hosts: []LocalHost{{
					Name:      "  host1  ",
					PrimaryIP: "  10.0.0.1  ",
					Zone:      "  us-east1-a  ",
				}},
			}},
			GCP:        []GCPBackend{{Name: "  gcp-a  ", Project: "  proj  ", Zone: "  us-east1  "}},
			AWS:        []AWSBackend{{Name: "  aws-a  ", Profile: "  default  ", Region: "  us-east-1  "}},
			Kubernetes: []KubernetesBackend{{Name: "  k8s-a  ", Context: "  ctx  "}},
			Consul:     []ConsulBackend{{Name: "  consul-a  ", Addr: "  http://consul  ", Token: "  tok  "}},
			Proxmox: []ProxmoxBackend{{
				Name: "  pmx-a  ", URL: "  https://pve  ",
				User: "  root@pam  ", Password: "  secret  ",
				TokenID: "  tid  ", TokenSecret: "  tsec  ",
				ExecMode: "  pve  ",
			}},
			TrueNAS: []TrueNASBackend{{
				Name: "  nas-a  ", URL: "  https://nas  ",
				Username: "  root  ", APIKey: "  key  ", SSHUser: "  admin  ",
			}},
			Docker: []DockerBackend{{
				Name: "  dk-a  ", Host: "  tcp://host  ", ViaLocal: "  local-a  ",
				Socket: "  /var/run/docker.sock  ", Platform: "  linux  ",
				RunAs: "  root  ", Mode: "  containers  ",
				CACert: "  /ca  ", Cert: "  /cert  ", Key: "  /key  ",
				ViaSSH: DockerViaSSH{Host: "  10.0.0.2  ", User: "  ec2  ", IdentityFile: "  ~/.ssh/id  "},
			}},
		},
	}

	f.Sanitize()

	if f.Defaults.SSHUser != "ops" {
		t.Errorf("Defaults.SSHUser = %q", f.Defaults.SSHUser)
	}
	if f.Defaults.CacheTTL != "10m" {
		t.Errorf("Defaults.CacheTTL = %q", f.Defaults.CacheTTL)
	}
	if f.Defaults.RecordDir != "/tmp/recs" {
		t.Errorf("Defaults.RecordDir = %q", f.Defaults.RecordDir)
	}
	if f.Defaults.SecretsProvider != "gcpkms://" {
		t.Errorf("Defaults.SecretsProvider = %q", f.Defaults.SecretsProvider)
	}
	if f.Plugins.Directory != "/plugins" {
		t.Errorf("Plugins.Directory = %q", f.Plugins.Directory)
	}
	if f.Backends.Local[0].Name != "local-a" {
		t.Errorf("Local[0].Name = %q", f.Backends.Local[0].Name)
	}
	if f.Backends.Local[0].Hosts[0].PrimaryIP != "10.0.0.1" {
		t.Errorf("Local[0].Hosts[0].PrimaryIP = %q", f.Backends.Local[0].Hosts[0].PrimaryIP)
	}
	if f.Backends.GCP[0].Project != "proj" {
		t.Errorf("GCP[0].Project = %q", f.Backends.GCP[0].Project)
	}
	if f.Backends.AWS[0].Region != "us-east-1" {
		t.Errorf("AWS[0].Region = %q", f.Backends.AWS[0].Region)
	}
	if f.Backends.Consul[0].Token != "tok" {
		t.Errorf("Consul[0].Token = %q", f.Backends.Consul[0].Token)
	}
	if f.Backends.Proxmox[0].ExecMode != "pve" {
		t.Errorf("Proxmox[0].ExecMode = %q", f.Backends.Proxmox[0].ExecMode)
	}
	if f.Backends.Proxmox[0].Password != "secret" {
		t.Errorf("Proxmox[0].Password = %q", f.Backends.Proxmox[0].Password)
	}
	if f.Backends.TrueNAS[0].APIKey != "key" {
		t.Errorf("TrueNAS[0].APIKey = %q", f.Backends.TrueNAS[0].APIKey)
	}
	if f.Backends.Docker[0].ViaSSH.Host != "10.0.0.2" {
		t.Errorf("Docker[0].ViaSSH.Host = %q", f.Backends.Docker[0].ViaSSH.Host)
	}
	if f.Backends.Docker[0].Key != "/key" {
		t.Errorf("Docker[0].Key = %q", f.Backends.Docker[0].Key)
	}
}
