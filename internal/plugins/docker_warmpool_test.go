package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	containertypes "github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

func baseWarmCfg() dockerTransportConfig {
	return dockerTransportConfig{
		Image:    "example/plugin:1.0",
		InitMode: "bind",
		Env:      map[string]string{"A": "1", "B": "2"},
		Volumes:  []string{"/host/x:/x:rw", "/host/y:/y:ro"},
		KeepWarm: true,
		PluginID: "my-plugin",
	}
}

func TestWarmDigest_StableAndOrderInsensitive(t *testing.T) {
	a := baseWarmCfg()
	b := baseWarmCfg()
	// Reorder env + volumes; digest must be identical (both sorted internally).
	b.Env = map[string]string{"B": "2", "A": "1"}
	b.Volumes = []string{"/host/y:/y:ro", "/host/x:/x:rw"}
	if warmDigest(a) != warmDigest(b) {
		t.Fatalf("digest not order-insensitive: %s vs %s", warmDigest(a), warmDigest(b))
	}
	if len(warmDigest(a)) != 16 {
		t.Fatalf("digest length = %d, want 16", len(warmDigest(a)))
	}
}

func TestWarmDigest_SensitiveToContainerIdentity(t *testing.T) {
	base := baseWarmCfg()
	baseD := warmDigest(base)

	mut := map[string]func(*dockerTransportConfig){
		"image":        func(c *dockerTransportConfig) { c.Image = "example/plugin:2.0" },
		"env value":    func(c *dockerTransportConfig) { c.Env = map[string]string{"A": "9", "B": "2"} },
		"env key":      func(c *dockerTransportConfig) { c.Env = map[string]string{"A": "1", "C": "2"} },
		"volumes":      func(c *dockerTransportConfig) { c.Volumes = []string{"/host/x:/x:rw"} },
		"init mode":    func(c *dockerTransportConfig) { c.InitMode = "embedded" },
		"init path":    func(c *dockerTransportConfig) { c.InitPath = "/opt/init" },
		"host network": func(c *dockerTransportConfig) { c.HostNetwork = true },
	}
	for name, f := range mut {
		c := baseWarmCfg()
		f(&c)
		if warmDigest(c) == baseD {
			t.Errorf("%s: digest unchanged (%s), want different", name, baseD)
		}
	}
}

func TestWarmDigest_IgnoresCueAndPluginID(t *testing.T) {
	base := baseWarmCfg()
	baseD := warmDigest(base)

	// CUE source is host-evaluated per call, not baked into the container.
	withCue := baseWarmCfg()
	withCue.CueSource = []byte("actions: {}")
	if warmDigest(withCue) != baseD {
		t.Error("cue source must not affect the container digest")
	}

	// Plugin id names the container but is not a container-identity input.
	withID := baseWarmCfg()
	withID.PluginID = "other"
	if warmDigest(withID) != baseD {
		t.Error("plugin id must not affect the container digest")
	}
}

func TestWarmContainerName_Sanitizes(t *testing.T) {
	cases := map[string]string{
		"my-plugin":   "honey-plugin-my-plugin-DIG",
		"aws/gcloud":  "honey-plugin-aws_gcloud-DIG",
		"a b c":       "honey-plugin-a_b_c-DIG",
		"weird:@id.1": "honey-plugin-weird__id.1-DIG",
		"":            "honey-plugin-plugin-DIG",
	}
	for in, want := range cases {
		got := warmContainerName(in, "DIG")
		if got != want {
			t.Errorf("warmContainerName(%q) = %q, want %q", in, got, want)
		}
		// Docker names must start with an alnum and contain only [a-zA-Z0-9_.-].
		if !isValidDockerName(got) {
			t.Errorf("warmContainerName(%q) = %q is not a valid docker name", in, got)
		}
	}
}

func isValidDockerName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-'
		if !ok {
			return false
		}
		if i == 0 && (r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

func TestWarmPublishedAddr(t *testing.T) {
	got, ok := warmPublishedAddr([]containerPortSummary{
		{PrivatePort: 1234, PublicPort: 55, Type: "tcp"},
		{PrivatePort: pluginInitContainerPort, PublicPort: 40001, Type: "tcp"},
	})
	if !ok || got != "http://127.0.0.1:40001" {
		t.Fatalf("got (%q,%v), want http://127.0.0.1:40001,true", got, ok)
	}
	if _, ok := warmPublishedAddr([]containerPortSummary{{PrivatePort: pluginInitContainerPort, PublicPort: 0}}); ok {
		t.Error("unpublished port should report ok=false")
	}
	if _, ok := warmPublishedAddr(nil); ok {
		t.Error("no ports should report ok=false")
	}
}

// fakeLister returns a fixed container list (or error).
type fakeLister struct {
	items    []containertypes.Summary
	err      error
	lastOpts client.ContainerListOptions
}

func (f *fakeLister) ContainerList(_ context.Context, opts client.ContainerListOptions) (client.ContainerListResult, error) {
	f.lastOpts = opts
	if f.err != nil {
		return client.ContainerListResult{}, f.err
	}
	return client.ContainerListResult{Items: f.items}, nil
}

// healthServer starts a loopback server answering /healthz with apiVersion and
// returns its host port.
func healthServer(t *testing.T, apiVersion string) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(apiv1.HealthResponse{APIVersion: apiVersion})
	}))
	t.Cleanup(srv.Close)
	_, portStr, err := splitHostPort(srv.URL)
	if err != nil {
		t.Fatalf("parse server url %q: %v", srv.URL, err)
	}
	p, _ := strconv.Atoi(portStr)
	return p
}

func splitHostPort(url string) (host, port string, err error) {
	url = strings.TrimPrefix(url, "http://")
	i := strings.LastIndex(url, ":")
	if i < 0 {
		return "", "", fmt.Errorf("no port in %q", url)
	}
	return url[:i], url[i+1:], nil
}

func summaryWithPort(id string, port int) containertypes.Summary {
	return containertypes.Summary{
		ID:     id,
		Labels: map[string]string{warmLabelManaged: "true", warmLabelDigest: "d"},
		Ports:  []containertypes.PortSummary{{PrivatePort: pluginInitContainerPort, PublicPort: uint16(port), Type: "tcp"}},
	}
}

func TestFindWarmContainer_ReusableWhenHealthy(t *testing.T) {
	port := healthServer(t, apiv1.APIVersion)
	lister := &fakeLister{items: []containertypes.Summary{summaryWithPort("c1", port)}}

	id, addr, reusable, staleID := findWarmContainer(context.Background(), lister, http.DefaultClient, "d")
	if !reusable || id != "c1" || addr != fmt.Sprintf("http://127.0.0.1:%d", port) || staleID != "" {
		t.Fatalf("got id=%q addr=%q reusable=%v stale=%q, want reusable c1", id, addr, reusable, staleID)
	}
	// Confirm the list was filtered by managed+digest+running.
	labels := lister.lastOpts.Filters["label"]
	if !labels[warmLabelManaged+"=true"] || !labels[warmLabelDigest+"=d"] {
		t.Errorf("list not filtered by warm labels: %v", lister.lastOpts.Filters)
	}
	if !lister.lastOpts.Filters["status"]["running"] {
		t.Errorf("list not filtered by status=running: %v", lister.lastOpts.Filters)
	}
}

func TestFindWarmContainer_StaleOnVersionMismatch(t *testing.T) {
	port := healthServer(t, "honey.plugins/OLD")
	lister := &fakeLister{items: []containertypes.Summary{summaryWithPort("c1", port)}}

	id, _, reusable, staleID := findWarmContainer(context.Background(), lister, http.DefaultClient, "d")
	if reusable || id != "" || staleID != "c1" {
		t.Fatalf("got id=%q reusable=%v stale=%q, want stale c1 (version mismatch)", id, reusable, staleID)
	}
}

func TestFindWarmContainer_NoneWhenUnreachableOrEmpty(t *testing.T) {
	// Unreachable published port (nothing listening): not reusable, not stale.
	lister := &fakeLister{items: []containertypes.Summary{summaryWithPort("c1", 1)}}
	if _, _, reusable, staleID := findWarmContainer(context.Background(), lister, http.DefaultClient, "d"); reusable || staleID != "" {
		t.Errorf("unreachable: got reusable=%v stale=%q, want none", reusable, staleID)
	}
	// Empty list.
	empty := &fakeLister{}
	if _, _, reusable, staleID := findWarmContainer(context.Background(), empty, http.DefaultClient, "d"); reusable || staleID != "" {
		t.Errorf("empty: got reusable=%v stale=%q, want none", reusable, staleID)
	}
	// List error → treated as no match.
	broken := &fakeLister{err: errors.New("daemon down")}
	if _, _, reusable, staleID := findWarmContainer(context.Background(), broken, http.DefaultClient, "d"); reusable || staleID != "" {
		t.Errorf("list error: got reusable=%v stale=%q, want none", reusable, staleID)
	}
}

// fakeReaper implements WarmReaperClient.
type fakeReaper struct {
	items     []containertypes.Summary
	removed   []string
	removeErr map[string]error
}

func (f *fakeReaper) ContainerList(_ context.Context, _ client.ContainerListOptions) (client.ContainerListResult, error) {
	return client.ContainerListResult{Items: f.items}, nil
}

func (f *fakeReaper) ContainerRemove(_ context.Context, id string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	if err := f.removeErr[id]; err != nil {
		return client.ContainerRemoveResult{}, err
	}
	f.removed = append(f.removed, id)
	return client.ContainerRemoveResult{}, nil
}

func TestReapWarmContainers_RemovesAllWhenNoTTL(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	f := &fakeReaper{items: []containertypes.Summary{
		{ID: "a", Created: now.Add(-time.Hour).Unix()},
		{ID: "b", Created: now.Add(-time.Minute).Unix()},
	}}
	n, err := ReapWarmContainers(context.Background(), f, 0, now)
	if err != nil || n != 2 || len(f.removed) != 2 {
		t.Fatalf("got n=%d err=%v removed=%v, want 2 removed", n, err, f.removed)
	}
}

func TestReapWarmContainers_OnlyOlderThan(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	f := &fakeReaper{items: []containertypes.Summary{
		{ID: "old", Created: now.Add(-2 * time.Hour).Unix()},
		{ID: "young", Created: now.Add(-10 * time.Minute).Unix()},
	}}
	n, err := ReapWarmContainers(context.Background(), f, time.Hour, now)
	if err != nil || n != 1 || len(f.removed) != 1 || f.removed[0] != "old" {
		t.Fatalf("got n=%d err=%v removed=%v, want only [old]", n, err, f.removed)
	}
}

func TestReapWarmContainers_AggregatesErrors(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	f := &fakeReaper{
		items:     []containertypes.Summary{{ID: "a"}, {ID: "b"}},
		removeErr: map[string]error{"a": errors.New("boom")},
	}
	n, err := ReapWarmContainers(context.Background(), f, 0, now)
	if n != 1 || err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got n=%d err=%v, want 1 removed + aggregated error", n, err)
	}
}

func TestIsWarmNameConflict(t *testing.T) {
	if !isWarmNameConflict(errors.New(`Conflict. The container name "/x" is already in use`)) {
		t.Error("should detect name conflict")
	}
	if isWarmNameConflict(errors.New("No such image")) {
		t.Error("should not treat unrelated error as name conflict")
	}
}
