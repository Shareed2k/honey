package sshclient

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const honeySSHOpenSSHGEnv = "HONEY_SSH_OPENSSH_G"

// hostSSHConfig holds ssh_config-derived settings for one logical target (leaf or hop),
// resolved either via system `ssh -G` (OpenSSH, includes Match) or kevinburke/ssh_config.
type hostSSHConfig struct {
	alias        string
	userOverride string
	fromOpenSSHG bool

	strictHostKeyChecking  string
	userKnownHostsFields   []string
	globalKnownHostsFields []string

	resolved      resolvedSSH
	proxyJump     string
	identityPaths []string
}

var opensshGSf singleflight.Group

func openSSHGDisabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(honeySSHOpenSSHGEnv)))
	return v == "0" || v == "false" || v == "no" || v == "off"
}

// sshGDestination is the argv tail passed to `ssh -G` after `--`.
func sshGDestination(userOverride, hostAlias string) string {
	hostAlias = strings.TrimSpace(hostAlias)
	u := strings.TrimSpace(userOverride)
	if u != "" {
		return fmt.Sprintf("%s@%s", u, hostAlias)
	}
	return hostAlias
}

func runSSHG(ctx context.Context, dest string) ([]byte, error) {
	return runSSHGWithEnv(ctx, dest, nil)
}

// runSSHGWithEnv runs `ssh -G` with optional Match-related environment variables.
func runSSHGWithEnv(ctx context.Context, dest string, matchEnv map[string]string) ([]byte, error) {
	if strings.TrimSpace(dest) == "" {
		return nil, fmt.Errorf("empty ssh -G destination")
	}
	// ssh -G prints canonical config; no network I/O to the remote host.
	// #nosec G204 -- fixed "ssh" binary and flags; dest is one argv field after "--" (no shell).
	cmd := exec.CommandContext(ctx, "ssh", "-G", "-T", "-o", "BatchMode=yes", "--", dest)
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	if len(matchEnv) > 0 {
		env := append([]string(nil), cmd.Env...)
		keys := make([]string, 0, len(matchEnv))
		for k := range matchEnv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env = append(env, k+"="+matchEnv[k])
		}
		cmd.Env = env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ssh -G %q: %w: %s", dest, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func runSSHGSingleflight(dest string) ([]byte, error) {
	v, err, _ := opensshGSf.Do(dest, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		return runSSHG(ctx, dest)
	})
	if err != nil {
		return nil, err
	}
	return v.([]byte), nil
}

type openSSHGParsed struct {
	user, hostname, port  string
	identityFiles         []string
	proxyJump             string
	strictHostKeyChecking string
	userKnownHosts        []string
	globalKnownHosts      []string
}

func parseSSHGOutput(data []byte) (openSSHGParsed, bool) {
	var p openSSHGParsed
	seen := false
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line == "" {
			continue
		}
		i := strings.IndexByte(line, ' ')
		if i <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		val := strings.TrimSpace(line[i+1:])
		switch key {
		case "user":
			p.user = val
			seen = true
		case "hostname":
			p.hostname = val
			seen = true
		case "port":
			p.port = val
			seen = true
		case "identityfile":
			if val != "" {
				p.identityFiles = append(p.identityFiles, val)
			}
			seen = true
		case "proxyjump":
			p.proxyJump = val
			seen = true
		case "stricthostkeychecking":
			p.strictHostKeyChecking = val
			seen = true
		case "userknownhostsfile":
			for _, part := range strings.Fields(val) {
				part = strings.TrimSpace(part)
				if part != "" {
					p.userKnownHosts = append(p.userKnownHosts, part)
				}
			}
			seen = true
		case "globalknownhostsfile":
			for _, part := range strings.Fields(val) {
				part = strings.TrimSpace(part)
				if part != "" {
					p.globalKnownHosts = append(p.globalKnownHosts, part)
				}
			}
			seen = true
		}
	}
	if strings.TrimSpace(p.hostname) == "" {
		return openSSHGParsed{}, false
	}
	return p, seen
}

func expandIdentityRawList(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var out []string
	for _, f := range raw {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		ep, err := expandSSHPath(f)
		if err != nil {
			return nil, err
		}
		if ep != "" {
			out = append(out, ep)
		}
	}
	return out, nil
}

func resolvedFromOpenSSH(alias, userOverride string, p openSSHGParsed) resolvedSSH {
	alias = strings.TrimSpace(alias)
	u0 := strings.TrimSpace(userOverride)

	hostName := strings.TrimSpace(p.hostname)
	if hostName == "" {
		hostName = alias
	}
	port := 22
	if ps := strings.TrimSpace(p.port); ps != "" {
		if po, err := strconv.Atoi(ps); err == nil && po > 0 && po < 65536 {
			port = po
		}
	}
	user := u0
	if user == "" {
		user = strings.TrimSpace(p.user)
	}
	if user == "" {
		user = defaultLocalUser()
	}
	return resolvedSSH{user: user, host: hostName, port: port}
}

func newHostSSHConfigBuiltin(alias, userOverride string) (*hostSSHConfig, error) {
	alias = strings.TrimSpace(alias)
	r := resolveOneBuiltin(alias, userOverride)
	idPaths, err := collectIdentityFilesBuiltin(alias)
	if err != nil {
		return nil, err
	}
	strict := strings.ToLower(strings.TrimSpace(honeySSHConfig.Get(alias, "StrictHostKeyChecking")))
	return &hostSSHConfig{
		alias:                 alias,
		userOverride:          userOverride,
		fromOpenSSHG:          false,
		strictHostKeyChecking: strict,
		resolved:              r,
		proxyJump:             strings.TrimSpace(honeySSHConfig.Get(alias, "ProxyJump")),
		identityPaths:         idPaths,
	}, nil
}

func newHostSSHConfigFromOpenSSH(alias, userOverride string, p openSSHGParsed) (*hostSSHConfig, error) {
	alias = strings.TrimSpace(alias)
	strict := strings.ToLower(strings.TrimSpace(p.strictHostKeyChecking))
	idPaths, err := expandIdentityRawList(p.identityFiles)
	if err != nil {
		return nil, err
	}
	return &hostSSHConfig{
		alias:                  alias,
		userOverride:           userOverride,
		fromOpenSSHG:           true,
		strictHostKeyChecking:  strict,
		userKnownHostsFields:   append([]string(nil), p.userKnownHosts...),
		globalKnownHostsFields: append([]string(nil), p.globalKnownHosts...),
		resolved:               resolvedFromOpenSSH(alias, userOverride, p),
		proxyJump:              strings.TrimSpace(p.proxyJump),
		identityPaths:          idPaths,
	}, nil
}

// lookupHostSSHConfig resolves ssh_config for (alias, userOverride) using OpenSSH `ssh -G`
// when enabled and available, otherwise kevinburke/ssh_config only.
func lookupHostSSHConfig(alias, userOverride string) (*hostSSHConfig, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return nil, fmt.Errorf("empty host alias")
	}
	if openSSHGDisabled() {
		return newHostSSHConfigBuiltin(alias, userOverride)
	}
	dest := sshGDestination(userOverride, alias)
	out, err := runSSHGSingleflight(dest)
	if err != nil {
		zap.L().Debug(
			"ssh -G failed; using builtin ssh_config parser",
			zap.String("dest", dest),
			zap.Error(err),
		)
		return newHostSSHConfigBuiltin(alias, userOverride)
	}
	parsed, ok := parseSSHGOutput(out)
	if !ok {
		zap.L().Debug("ssh -G parse unusable; using builtin ssh_config parser", zap.String("dest", dest))
		return newHostSSHConfigBuiltin(alias, userOverride)
	}
	return newHostSSHConfigFromOpenSSH(alias, userOverride, parsed)
}
