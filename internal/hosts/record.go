package hosts

import "strings"

// Record is a normalized host across cloud providers.
type Record struct {
	Provider  string            `json:"provider"`
	Name      string            `json:"name"`
	PrimaryIP string            `json:"primary_ip"`
	ExtraIPs  []string          `json:"extra_ips,omitempty"`
	Zone      string            `json:"zone,omitempty"`
	Region    string            `json:"region,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// IsDockerRecord reports whether r is a connectable Docker container or swarm task row.
func IsDockerRecord(r Record) bool {
	if r.Provider != "docker" {
		return false
	}
	k := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	return k == "container" || k == "swarm_task"
}

// IsConnectableRecord reports whether honey can exec, upload, or open a terminal on r.
func IsConnectableRecord(r Record) bool {
	if IsDockerRecord(r) {
		return strings.TrimSpace(r.Meta["container_id"]) != ""
	}
	if strings.TrimSpace(r.PrimaryIP) != "" {
		return true
	}
	return r.Provider == "k8s" && strings.EqualFold(strings.TrimSpace(r.Meta["kind"]), "pod")
}
