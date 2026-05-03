package searchrun

// ProviderFlags carries CLI / MCP provider-related string options used when
// building backends (mirrors honey search flags).
type ProviderFlags struct {
	GCPProject       string
	GCPZone          string
	AWSProfile       string
	AWSRegion        string
	KubeContext      string
	K8sMode          string
	K8sDebugImage    string
	Kubeconfig       string
	ConsulAddr       string
	ConsulDatacenter string
	ConsulToken      string

	ProxmoxURL         string
	ProxmoxUser        string
	ProxmoxPassword    string
	ProxmoxTokenID     string
	ProxmoxTokenSecret string
	ProxmoxInsecure    bool
}
