package hosts

// Query carries global and per-provider filters for a search run.
type Query struct {
	NameSubstring string
	NameRegex     string
	Providers     []string // e.g. gcp, aws, k8s, consul — empty means all

	GCPProject string
	GCPZone    string

	AWSProfile string
	AWSRegion  string

	KubeContext   string
	K8sMode       string
	K8sDebugImage string

	ConsulAddr       string
	ConsulDatacenter string
	ConsulToken      string
}
