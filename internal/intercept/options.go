package intercept

import (
	"errors"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

// defaultInterceptShell is the command run when the caller supplies none.
var defaultInterceptShell = []string{"/bin/sh"}

// OptionsFromPodRecord maps a validated Kubernetes pod record to targeted
// interception Options. It is the single mapping used by both the web fallback
// handler and the intercept-pane subcommand so they stay in lockstep.
func OptionsFromPodRecord(rec hosts.Record, modes []string, udp bool, command []string, agentImage string) (Options, error) {
	namespace := strings.TrimSpace(rec.Meta["namespace"])
	pod := strings.TrimSpace(rec.Meta["pod_name"])
	if namespace == "" || pod == "" {
		return Options{}, errors.New("intercept: pod record missing namespace or pod_name")
	}
	if strings.TrimSpace(agentImage) == "" {
		return Options{}, errors.New("intercept: no agent image configured (set intercept.agent_image)")
	}
	parsed, err := ParseModes(modes)
	if err != nil {
		return Options{}, err
	}
	if len(command) == 0 {
		command = defaultInterceptShell
	}
	return Options{
		Namespace:  namespace,
		Pod:        pod,
		Cluster:    strings.TrimSpace(rec.Meta["kube_context"]),
		AgentImage: strings.TrimSpace(agentImage),
		Modes:      parsed,
		UDP:        udp,
		Command:    command,
		Targetless: false,
	}, nil
}
