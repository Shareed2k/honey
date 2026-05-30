package dockerprovider

import "github.com/spf13/cobra"

var cliFlags struct {
	host          string
	mode          string
	allContainers bool
	viaLocal      string
	viaSSHHost    string
	socket        string
	platform      string
}

// RegisterFlags adds Docker CLI flags to cmd.
func RegisterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cliFlags.host, "docker-host", "", "Docker host (unix://, tcp://, ssh://; default: DOCKER_HOST / local socket)")
	cmd.Flags().StringVar(&cliFlags.mode, "docker-mode", "containers", "Docker search mode: containers, swarm, or both")
	cmd.Flags().BoolVar(&cliFlags.allContainers, "docker-all", false, "Include stopped containers in docker search")
	cmd.Flags().StringVar(&cliFlags.viaLocal, "docker-via-local", "", "Docker via Honey SSH: backends.local name")
	cmd.Flags().StringVar(&cliFlags.viaSSHHost, "docker-via-ssh-host", "", "Docker via Honey SSH: explicit host")
	cmd.Flags().StringVar(&cliFlags.socket, "docker-socket", "", "Remote Docker socket (default /var/run/docker.sock on linux)")
	cmd.Flags().StringVar(&cliFlags.platform, "docker-platform", "linux", "Remote Docker host OS: linux or windows")
}
