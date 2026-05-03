package hosts

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
