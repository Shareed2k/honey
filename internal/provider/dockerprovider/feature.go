package dockerprovider

import "os"

// FeatureDockerViaProviders reports whether auto-discover on cloud VMs is enabled.
func FeatureDockerViaProviders() bool {
	return os.Getenv("HONEY_FEATURE_DOCKER_VIA_PROVIDERS") == "1"
}
