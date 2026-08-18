package intercept

import (
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/stretchr/testify/require"
)

func podRecord() hosts.Record {
	return hosts.Record{Provider: "k8s", Meta: map[string]string{
		"kind": "pod", "namespace": "argocd", "pod_name": "api-0", "kube_context": "stg2",
	}}
}

func TestOptionsFromPodRecord(t *testing.T) {
	got, err := OptionsFromPodRecord(podRecord(), []string{"egress"}, false, nil, "img:1")
	require.NoError(t, err)
	require.Equal(t, "argocd", got.Namespace)
	require.Equal(t, "api-0", got.Pod)
	require.Equal(t, "stg2", got.Cluster)
	require.Equal(t, "img:1", got.AgentImage)
	require.Equal(t, []string{"/bin/sh"}, got.Command) // defaulted
	require.False(t, got.Targetless)
	require.True(t, got.Modes.Egress)
}

func TestOptionsFromPodRecord_Errors(t *testing.T) {
	_, err := OptionsFromPodRecord(hosts.Record{Meta: map[string]string{"pod_name": "p"}}, []string{"egress"}, false, nil, "img")
	require.ErrorContains(t, err, "namespace or pod_name")
	_, err = OptionsFromPodRecord(podRecord(), []string{"egress"}, false, nil, "")
	require.ErrorContains(t, err, "no agent image")
	_, err = OptionsFromPodRecord(podRecord(), []string{"bogus"}, false, nil, "img")
	require.ErrorContains(t, err, "unknown mode")
}
