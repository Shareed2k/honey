package cli

import "github.com/shareed2k/honey/internal/searchrun"

func providerFlagsSnapshot() searchrun.ProviderFlags {
	return searchrun.ProviderFlags{
		GCPProject:          flagGCPProject,
		GCPZone:             flagGCPZone,
		AWSProfile:          flagAWSProfile,
		AWSRegion:           flagAWSRegion,
		KubeContext:         flagKubeContext,
		K8sMode:             flagK8sMode,
		K8sDebugImage:       flagK8sDebugImg,
		Kubeconfig:          flagKubeconfig,
		ConsulAddr:          flagConsulAddr,
		ConsulDatacenter:    flagConsulDC,
		ConsulToken:         flagConsulToken,
		ProxmoxURL:          flagProxmoxURL,
		ProxmoxUser:         flagProxmoxUser,
		ProxmoxPassword:     flagProxmoxPassword,
		ProxmoxTokenID:      flagProxmoxTokenID,
		ProxmoxTokenSecret:  flagProxmoxTokenSecret,
		ProxmoxInsecure:     flagProxmoxInsecure,
		TrueNASURL:          flagTrueNASURL,
		TrueNASUser:         flagTrueNASUser,
		TrueNASAPIKey:       flagTrueNASAPIKey,
		TrueNASInsecure:     flagTrueNASInsecure,
		DockerHost:          flagDockerHost,
		DockerMode:          flagDockerMode,
		DockerAllContainers: flagDockerAll,
		DockerViaLocal:      flagDockerViaLocal,
		DockerViaSSHHost:    flagDockerViaSSHHost,
		DockerSocket:        flagDockerSocket,
		DockerPlatform:      flagDockerPlatform,
	}
}
