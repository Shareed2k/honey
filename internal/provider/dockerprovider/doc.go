// Package dockerprovider implements the honey Docker Engine search backend.
//
// Search modes (YAML mode or --docker-mode): containers, swarm, or both.
// Results are hosts.Record rows with provider "docker" and meta.kind
// container or swarm_task.
//
// Daemon connections:
//   - Local or DOCKER_HOST (unix://, tcp://, https:// with optional TLS paths in config)
//   - Moby ssh:// (Engine SDK SSH; no Honey ~/.ssh/config integration)
//   - Honey SSH: via_local / via_ssh + socket + optional run_as (sudo -n -u … +
//     docker system dial-stdio over an sshclient session; reuses TUI SSH when cached)
//
// Auto-discover registers with searchrun.RegisterDockerDiscover. When
// HONEY_FEATURE_DOCKER_VIA_PROVIDERS=1 and --docker-discover-providers are set,
// searchrun runs a second pass over VM records from the already-filtered search
// (respecting --backends and --provider) and merges container rows.
//
// Execution (terminals, parallel e, web exec, file browser) is wired through
// hostexec.SetDockerExecutor in internal/ui; the Moby client is
// github.com/moby/moby/client (API types in github.com/moby/moby/api).
package dockerprovider
