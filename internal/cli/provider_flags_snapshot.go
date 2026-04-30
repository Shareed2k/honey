package cli

import "honey/internal/searchrun"

func providerFlagsSnapshot() searchrun.ProviderFlags {
	return searchrun.ProviderFlags{
		GCPProject:       flagGCPProject,
		GCPZone:          flagGCPZone,
		AWSProfile:       flagAWSProfile,
		AWSRegion:        flagAWSRegion,
		KubeContext:      flagKubeContext,
		K8sMode:          flagK8sMode,
		Kubeconfig:       flagKubeconfig,
		ConsulAddr:       flagConsulAddr,
		ConsulDatacenter: flagConsulDC,
		ConsulToken:      flagConsulToken,
	}
}
