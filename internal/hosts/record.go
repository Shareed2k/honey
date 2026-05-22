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
	if k == "container" || k == "swarm_task" {
		return true
	}
	// Rows from older builds or clients that omit meta.kind but include container_id.
	return strings.TrimSpace(r.Meta["container_id"]) != ""
}

// IsTrueNASAPIShellRecord reports whether r is a TrueNAS row that can use /websocket/shell
// (shape only; backend config is checked at runtime).
func IsTrueNASAPIShellRecord(r Record) bool {
	if r.Provider != "truenas" {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	switch kind {
	case "appliance":
		return true
	case "virt_instance":
		return strings.TrimSpace(r.Meta["id"]) != ""
	case "vm":
		if strings.TrimSpace(r.Meta["virt_instance_id"]) != "" {
			return true
		}
		return strings.TrimSpace(r.Name) != ""
	default:
		return false
	}
}

// PrimaryIPTrimmed returns r.PrimaryIP with surrounding whitespace removed.
func PrimaryIPTrimmed(r Record) string {
	return strings.TrimSpace(r.PrimaryIP)
}

// IsConnectableRecord reports whether honey can exec, upload, or open a terminal on r.
func IsConnectableRecord(r Record) bool {
	if IsDockerRecord(r) {
		return strings.TrimSpace(r.Meta["container_id"]) != ""
	}
	if IsTrueNASAPIShellRecord(r) {
		return true
	}
	if strings.TrimSpace(r.PrimaryIP) != "" {
		return true
	}
	return r.Provider == "k8s" && strings.EqualFold(strings.TrimSpace(r.Meta["kind"]), "pod")
}
