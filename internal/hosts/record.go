package hosts

// Record is a normalized host across cloud providers.
type Record struct {
	Provider  string
	Name      string
	PrimaryIP string
	ExtraIPs  []string
	Zone      string
	Region    string
	Meta      map[string]string
}
