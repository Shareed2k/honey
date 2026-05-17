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

	ProxmoxURL         string
	ProxmoxUser        string
	ProxmoxPassword    string
	ProxmoxTokenID     string
	ProxmoxTokenSecret string
	ProxmoxInsecure    bool

	DockerHost              string
	DockerMode              string
	DockerAllContainers     bool
	DockerViaLocal          string
	DockerViaSSHHost        string
	DockerSocket            string
	DockerPlatform          string
	DockerDiscoverProviders []string // feature-flag only; provider ids e.g. gcp, aws
	DockerSSHUser           string
	DockerDiscoverRunAs     string // sudo -u user for docker.sock when SSH user lacks access
}
