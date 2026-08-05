package plugins

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"go.uber.org/zap"
)

// Warm-pool: opt-in reuse of docker-runtime plugin containers across CLI runs.
//
// Normally a plugin container is anonymous and torn down on Manager.Close, so a
// fresh `honey` run re-pays cold container start. With plugins.keep_warm, the
// container is created with a deterministic name + labels; a later run finds the
// running container, health-checks it (the api_version handshake), and attaches
// to its published port instead of creating a new one. Close leaves warm
// containers running; `honey plugins gc` reaps them.
//
// Container identity is exactly what is baked into the container — image,
// entrypoint (init mode/path), env, volumes, network mode — captured as a
// digest. The plugin's CUE source is deliberately excluded: it is evaluated
// host-side per call (execAction sends host-computed argv/env/stdin to the
// shim), so it never affects container reuse. api_version is excluded too and
// left to the health check, which self-heals a version-skewed warm container by
// forcing a replace (see findWarmContainer / createAndStart).
const (
	warmLabelManaged    = "honey.plugin.managed"     // "true" on every warm container
	warmLabelPluginID   = "honey.plugin.id"          // manifest id
	warmLabelDigest     = "honey.plugin.digest"      // config digest (see warmDigest)
	warmLabelAPIVersion = "honey.plugin.api_version" // honey's api_version at create time (informational)
)

// containerLister is the minimal subset of *client.Client the warm-pool needs
// to discover containers. *client.Client satisfies it structurally, so
// production passes it directly while tests fake just this one method.
type containerLister interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
}

// warmDigest is a stable hash of the container-identity inputs of cfg: two
// configs that would produce interchangeable containers share a digest; any
// difference that changes what runs inside the container changes it. Env and
// volumes are sorted so ordering never perturbs the digest.
func warmDigest(cfg dockerTransportConfig) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			h.Write([]byte(p))
			h.Write([]byte{0})
		}
	}
	write("image", cfg.Image)
	write("init_mode", cfg.InitMode)
	write("init_path", cfg.InitPath)
	write("host_network", strconv.FormatBool(cfg.HostNetwork))

	env := envSlice(cfg.Env) // already sorted by name
	write("env")
	write(env...)

	vols := append([]string(nil), cfg.Volumes...)
	sort.Strings(vols)
	write("volumes")
	write(vols...)

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// warmContainerName is the deterministic container name for a plugin+digest:
// honey-plugin-<sanitized-id>-<digest>. Docker names must match
// [a-zA-Z0-9][a-zA-Z0-9_.-]+, so any other rune in the plugin id becomes '_'.
func warmContainerName(pluginID, digest string) string {
	var b strings.Builder
	for _, r := range pluginID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	id := b.String()
	if id == "" {
		id = "plugin"
	}
	return "honey-plugin-" + id + "-" + digest
}

// warmLabels are the labels stamped on a warm container so later runs (reuse)
// and `honey plugins gc` (reaping) can find it.
func warmLabels(pluginID, digest, apiVersion string) map[string]string {
	return map[string]string{
		warmLabelManaged:    "true",
		warmLabelPluginID:   pluginID,
		warmLabelDigest:     digest,
		warmLabelAPIVersion: apiVersion,
	}
}

// warmPublishedAddr returns the loopback http address of a running container's
// published shim port (pluginInitContainerPort), from a container list summary.
// Reports ok=false when the port is not (yet) published.
func warmPublishedAddr(ports []containerPortSummary) (string, bool) {
	for _, p := range ports {
		if p.PrivatePort == pluginInitContainerPort && p.PublicPort != 0 && (p.Type == "" || p.Type == "tcp") {
			return fmt.Sprintf("http://127.0.0.1:%d", p.PublicPort), true
		}
	}
	return "", false
}

// containerPortSummary mirrors the fields of container.PortSummary the warm-pool
// reads. A tiny local shape keeps warmPublishedAddr unit-testable without
// constructing moby API structs.
type containerPortSummary struct {
	PrivatePort uint16
	PublicPort  uint16
	Type        string
}

// findWarmContainer looks for a running warm container for digest and reports
// whether it is reusable. Return values:
//
//	reusable=true              → attach to it (id + addr); no create needed.
//	reusable=false, staleID!="" → a matching container exists but is not usable
//	                             (unreachable, or api_version mismatch); the
//	                             caller should force-remove staleID and recreate.
//	reusable=false, staleID=""  → nothing found; create fresh.
func findWarmContainer(ctx context.Context, lister containerLister, httpClient *http.Client, digest string) (id, addr string, reusable bool, staleID string) {
	filters := client.Filters{}.
		Add("label", warmLabelManaged+"=true").
		Add("label", warmLabelDigest+"="+digest).
		Add("status", "running")

	res, err := lister.ContainerList(ctx, client.ContainerListOptions{Filters: filters})
	if err != nil {
		zap.L().Warn("plugins: warm-pool list failed, will create fresh container", zap.Error(err))
		return "", "", false, ""
	}

	for _, c := range res.Items {
		ports := make([]containerPortSummary, len(c.Ports))
		for i, p := range c.Ports {
			ports[i] = containerPortSummary{PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Type: p.Type}
		}
		candidate, ok := warmPublishedAddr(ports)
		if !ok {
			continue
		}
		ready, fatal := checkHealth(ctx, httpClient, candidate)
		if ready {
			return c.ID, candidate, true, ""
		}
		if fatal != nil {
			// Reachable but api_version mismatch (or another hard error): the
			// warm container is a stale/incompatible build. Report it so the
			// caller replaces it rather than reusing a mismatched shim.
			zap.L().Info("plugins: warm container incompatible, will replace",
				zap.String("container_id", c.ID), zap.Error(fatal))
			return "", "", false, c.ID
		}
	}
	return "", "", false, ""
}

// ReapWarmContainers removes honey warm plugin containers. When olderThan is 0
// it removes all of them; otherwise only those created more than olderThan ago
// (by the daemon's Created timestamp). Returns the number removed. now is the
// reference time (pass time.Now()); it is a parameter so the selection logic is
// unit-testable. Errors removing individual containers are aggregated so one
// failure does not abort the sweep.
func ReapWarmContainers(ctx context.Context, cli WarmReaperClient, olderThan time.Duration, now time.Time) (int, error) {
	filters := client.Filters{}.Add("label", warmLabelManaged+"=true")
	res, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filters})
	if err != nil {
		return 0, fmt.Errorf("plugins: warm-pool list for gc: %w", err)
	}

	var (
		removed int
		errs    []error
	)
	for _, c := range res.Items {
		if olderThan > 0 {
			age := now.Sub(time.Unix(c.Created, 0))
			if age < olderThan {
				continue
			}
		}
		if _, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true}); err != nil {
			errs = append(errs, fmt.Errorf("remove %s: %w", c.ID, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(errs...)
}

// WarmReaperClient is the docker-client subset ReapWarmContainers needs.
// *client.Client satisfies it; tests fake it.
type WarmReaperClient interface {
	containerLister
	containerRemover
}

// GCWarmContainers removes keep_warm plugin containers from a docker daemon.
// socket overrides the ambient DOCKER_HOST when non-empty. olderThan==0 removes
// every warm container; otherwise only those created more than olderThan ago.
// Returns the number removed. This is the entry point behind `honey plugins gc`.
func GCWarmContainers(ctx context.Context, socket string, olderThan time.Duration) (int, error) {
	backend, err := newLocalBackend("", socket)
	if err != nil {
		return 0, err
	}
	defer func() { _ = backend.Close() }()
	return ReapWarmContainers(ctx, backend.Client(), olderThan, time.Now())
}

// isWarmNameConflict reports whether a ContainerCreate error is the daemon
// rejecting a duplicate deterministic name (a leftover container from a prior
// run holds it) — the signal to force-remove that container and retry.
func isWarmNameConflict(err error) bool {
	s := err.Error()
	return strings.Contains(s, "is already in use") || strings.Contains(s, "Conflict")
}

// forceRemoveContainer removes idOrName (a name is accepted by the daemon just
// like an id), forcing removal so a running container is reaped too.
func forceRemoveContainer(ctx context.Context, remover containerRemover, idOrName string) error {
	_, err := remover.ContainerRemove(ctx, idOrName, client.ContainerRemoveOptions{RemoveVolumes: true, Force: true})
	return err
}
