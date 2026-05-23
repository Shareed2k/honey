package sshclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/safepath"
	"golang.org/x/sync/singleflight"
)

const (
	forwardSourceOpenSSHG       = "ssh -G"
	forwardSourceFallbackParser = "fallback_parser"
)

// ForwardKind identifies an SSH port-forwarding directive.
type ForwardKind int

// ForwardKind values identify SSH config forward directive types.
const (
	ForwardKindLocal ForwardKind = iota
	ForwardKindRemote
	ForwardKindDynamic
)

// ForwardSpec is one LocalForward, RemoteForward, or DynamicForward entry.
type ForwardSpec struct {
	Kind ForwardKind

	// BindHost is the local bind address for Local/Dynamic forwards, or the
	// remote bind address for RemoteForward when set.
	BindHost string
	BindPort int

	// RemoteHost/RemotePort are the destination for LocalForward.
	RemoteHost string
	RemotePort int

	// LocalHost/LocalPort are the local-side target for RemoteForward.
	LocalHost string
	LocalPort int

	Source       string
	FallbackWarn bool
}

// ForwardSet holds parsed forwards grouped by kind.
type ForwardSet struct {
	Local   []ForwardSpec
	Remote  []ForwardSpec
	Dynamic []ForwardSpec
}

// All returns every forward in a stable order: local, remote, dynamic.
func (s ForwardSet) All() []ForwardSpec {
	out := make([]ForwardSpec, 0, len(s.Local)+len(s.Remote)+len(s.Dynamic))
	out = append(out, s.Local...)
	out = append(out, s.Remote...)
	out = append(out, s.Dynamic...)
	return out
}

var forwardsSf singleflight.Group

var (
	forwardConfigLineRe = regexp.MustCompile(`(?i)^\s*(LocalForward|RemoteForward|DynamicForward)\s+(.+)\s*$`)
	hostLineRe          = regexp.MustCompile(`(?i)^\s*Host\s+(.+)\s*$`)
	matchLineRe         = regexp.MustCompile(`(?i)^\s*Match\s+`)
	includeLineRe       = regexp.MustCompile(`(?i)^\s*Include\s+(.+)\s*$`)
)

// ParseOpenSSHGForwards parses localforward/remoteforward/dynamicforward lines from ssh -G output.
func ParseOpenSSHGForwards(data []byte) ForwardSet {
	var set ForwardSet
	for _, line := range strings.Split(string(data), "\n") {
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
		case "localforward":
			spec, err := parseOpenSSHGLocalForward(val)
			if err == nil {
				spec.Source = forwardSourceOpenSSHG
				set.Local = append(set.Local, spec)
			}
		case "remoteforward":
			spec, err := parseOpenSSHGRemoteForward(val)
			if err == nil {
				spec.Source = forwardSourceOpenSSHG
				set.Remote = append(set.Remote, spec)
			}
		case "dynamicforward":
			spec, err := parseOpenSSHGDynamicForward(val)
			if err == nil {
				spec.Source = forwardSourceOpenSSHG
				set.Dynamic = append(set.Dynamic, spec)
			}
		}
	}
	return set
}

func parseOpenSSHGLocalForward(val string) (ForwardSpec, error) {
	parts := strings.Fields(val)
	if len(parts) != 2 {
		return ForwardSpec{}, fmt.Errorf("localforward: want 2 fields, got %d", len(parts))
	}
	bindHost, bindPort, err := parseListenEndpoint(parts[0])
	if err != nil {
		return ForwardSpec{}, err
	}
	remoteHost, remotePort, err := parseDestEndpoint(parts[1])
	if err != nil {
		return ForwardSpec{}, err
	}
	return ForwardSpec{
		Kind:       ForwardKindLocal,
		BindHost:   bindHost,
		BindPort:   bindPort,
		RemoteHost: remoteHost,
		RemotePort: remotePort,
	}, nil
}

func parseOpenSSHGRemoteForward(val string) (ForwardSpec, error) {
	parts := strings.Fields(val)
	if len(parts) != 2 {
		return ForwardSpec{}, fmt.Errorf("remoteforward: want 2 fields, got %d", len(parts))
	}
	bindPort, err := strconv.Atoi(unbracket(parts[0]))
	if err != nil || bindPort <= 0 || bindPort >= 65536 {
		return ForwardSpec{}, fmt.Errorf("remoteforward bind port: %q", parts[0])
	}
	localHost, localPort, err := parseDestEndpoint(parts[1])
	if err != nil {
		return ForwardSpec{}, err
	}
	return ForwardSpec{
		Kind:      ForwardKindRemote,
		BindPort:  bindPort,
		LocalHost: localHost,
		LocalPort: localPort,
	}, nil
}

func parseOpenSSHGDynamicForward(val string) (ForwardSpec, error) {
	bindHost, bindPort, err := parseListenEndpoint(strings.TrimSpace(val))
	if err != nil {
		return ForwardSpec{}, err
	}
	return ForwardSpec{
		Kind:     ForwardKindDynamic,
		BindHost: bindHost,
		BindPort: bindPort,
	}, nil
}

// ParseForwardSpecLine parses one OpenSSH config forward directive line.
func ParseForwardSpecLine(line string) (ForwardSpec, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return ForwardSpec{}, fmt.Errorf("empty forward line")
	}
	m := forwardConfigLineRe.FindStringSubmatch(line)
	if m == nil {
		return ForwardSpec{}, fmt.Errorf("not a forward line: %q", line)
	}
	kindWord := strings.ToLower(strings.TrimSpace(m[1]))
	rest := strings.TrimSpace(m[2])
	switch kindWord {
	case "localforward":
		spec, err := parseConfigLocalForward(rest)
		if err != nil {
			return ForwardSpec{}, err
		}
		spec.Kind = ForwardKindLocal
		return spec, nil
	case "remoteforward":
		spec, err := parseConfigRemoteForward(rest)
		if err != nil {
			return ForwardSpec{}, err
		}
		spec.Kind = ForwardKindRemote
		return spec, nil
	case "dynamicforward":
		spec, err := parseConfigDynamicForward(rest)
		if err != nil {
			return ForwardSpec{}, err
		}
		spec.Kind = ForwardKindDynamic
		return spec, nil
	default:
		return ForwardSpec{}, fmt.Errorf("unknown forward kind %q", kindWord)
	}
}

func parseConfigLocalForward(rest string) (ForwardSpec, error) {
	listener, dest, ok := splitForwardPair(rest)
	if !ok {
		return ForwardSpec{}, fmt.Errorf("localforward: %q", rest)
	}
	bindHost, bindPort, err := parseListenEndpoint(listener)
	if err != nil {
		return ForwardSpec{}, err
	}
	remoteHost, remotePort, err := parseDestEndpoint(dest)
	if err != nil {
		return ForwardSpec{}, err
	}
	return ForwardSpec{
		BindHost:   bindHost,
		BindPort:   bindPort,
		RemoteHost: remoteHost,
		RemotePort: remotePort,
	}, nil
}

func parseConfigRemoteForward(rest string) (ForwardSpec, error) {
	listener, dest, ok := splitForwardPair(rest)
	if !ok {
		return ForwardSpec{}, fmt.Errorf("remoteforward: %q", rest)
	}
	bindPort, err := strconv.Atoi(unbracket(listener))
	if err != nil || bindPort <= 0 || bindPort >= 65536 {
		return ForwardSpec{}, fmt.Errorf("remoteforward bind port: %q", listener)
	}
	localHost, localPort, err := parseDestEndpoint(dest)
	if err != nil {
		return ForwardSpec{}, err
	}
	return ForwardSpec{
		BindPort:  bindPort,
		LocalHost: localHost,
		LocalPort: localPort,
	}, nil
}

func parseConfigDynamicForward(rest string) (ForwardSpec, error) {
	bindHost, bindPort, err := parseListenEndpoint(strings.TrimSpace(rest))
	if err != nil {
		return ForwardSpec{}, err
	}
	return ForwardSpec{
		BindHost: bindHost,
		BindPort: bindPort,
	}, nil
}

func splitForwardPair(rest string) (listener, dest string, ok bool) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", false
	}
	if i := strings.LastIndex(rest, " "); i > 0 {
		return strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+1:]), true
	}
	return "", "", false
}

func parseListenEndpoint(s string) (bindHost string, port int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("empty listen endpoint")
	}
	host, portStr, split := splitHostPortBracketed(s)
	if !split {
		p, aerr := strconv.Atoi(unbracket(s))
		if aerr != nil || p <= 0 || p >= 65536 {
			return "", 0, fmt.Errorf("listen port: %q", s)
		}
		return "", p, nil
	}
	p, aerr := strconv.Atoi(portStr)
	if aerr != nil || p <= 0 || p >= 65536 {
		return "", 0, fmt.Errorf("listen port: %q", s)
	}
	return unbracket(host), p, nil
}

func parseDestEndpoint(s string) (host string, port int, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", 0, fmt.Errorf("empty destination")
	}
	h, portStr, split := splitHostPortBracketed(s)
	if !split {
		return "", 0, fmt.Errorf("destination: %q", s)
	}
	p, aerr := strconv.Atoi(portStr)
	if aerr != nil || p <= 0 || p >= 65536 {
		return "", 0, fmt.Errorf("destination port: %q", s)
	}
	return unbracket(h), p, nil
}

func splitHostPortBracketed(s string) (host, port string, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", false
	}
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end <= 1 {
			return "", "", false
		}
		host = s[1:end]
		rest := strings.TrimSpace(s[end+1:])
		if !strings.HasPrefix(rest, ":") {
			return "", "", false
		}
		return host, strings.TrimSpace(rest[1:]), true
	}
	if h, p, err := net.SplitHostPort(s); err == nil {
		return h, p, true
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		return s[:i], s[i+1:], true
	}
	return "", "", false
}

func unbracket(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") && len(s) >= 2 {
		return s[1 : len(s)-1]
	}
	return s
}

// PickForward selects a forward from specs by bind port or remote port (match is a decimal port string).
func PickForward(specs []ForwardSpec, match string) (ForwardSpec, error) {
	match = strings.TrimSpace(match)
	if match == "" {
		return ForwardSpec{}, fmt.Errorf("empty forward match")
	}
	port, err := strconv.Atoi(match)
	if err != nil || port <= 0 || port >= 65536 {
		return ForwardSpec{}, fmt.Errorf("forward match must be a port number, got %q", match)
	}
	for _, spec := range specs {
		if spec.BindPort == port || spec.RemotePort == port || spec.LocalPort == port {
			return spec, nil
		}
	}
	return ForwardSpec{}, fmt.Errorf("no forward matching port %q", match)
}

// ForwardsForHost resolves LocalForward/RemoteForward/DynamicForward for alias using ssh -G,
// or a fallback parser when HONEY_SSH_OPENSSH_G disables ssh -G.
func ForwardsForHost(alias, user string, matchEnv map[string]string) (ForwardSet, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return ForwardSet{}, fmt.Errorf("empty host alias")
	}
	if openSSHGDisabled() {
		return forwardsFromSSHConfigFallback(alias)
	}
	dest := sshGDestination(user, alias)
	cacheKey := forwardsCacheKey(dest, matchEnv)
	v, err, _ := forwardsSf.Do(cacheKey, func() (any, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		out, runErr := runSSHGWithEnv(ctx, dest, matchEnv)
		if runErr != nil {
			return ForwardSet{}, runErr
		}
		return ParseOpenSSHGForwards(out), nil
	})
	if err != nil {
		return ForwardSet{}, err
	}
	return v.(ForwardSet), nil
}

func forwardsCacheKey(dest string, matchEnv map[string]string) string {
	if len(matchEnv) == 0 {
		return dest
	}
	keys := make([]string, 0, len(matchEnv))
	for k := range matchEnv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(dest)
	b.WriteByte(0)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(matchEnv[k])
		b.WriteByte(';')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return dest + "\x00" + hex.EncodeToString(sum[:8])
}

func forwardsFromSSHConfigFallback(alias string) (ForwardSet, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return ForwardSet{}, err
	}
	sshRoot, err := filepath.Abs(filepath.Join(home, ".ssh"))
	if err != nil {
		return ForwardSet{}, err
	}
	rootConfig := filepath.Join(sshRoot, "config")
	files, err := collectSSHConfigIncludes(sshRoot, rootConfig, 0)
	if err != nil {
		return ForwardSet{}, err
	}
	var set ForwardSet
	for _, path := range files {
		part, perr := parseForwardsFromConfigFile(sshRoot, path, alias)
		if perr != nil {
			return ForwardSet{}, perr
		}
		set.Local = append(set.Local, part.Local...)
		set.Remote = append(set.Remote, part.Remote...)
		set.Dynamic = append(set.Dynamic, part.Dynamic...)
	}
	return set, nil
}

func isSSHConfigGlob(path string) bool {
	return strings.ContainsAny(path, "*?[")
}

// sshConfigPathUnderRoot resolves path and ensures it stays within sshRoot.
// Glob patterns are returned unresolved; callers must validate each match.
func sshConfigPathUnderRoot(sshRoot, path string) (string, error) {
	path, err := expandSSHPath(path)
	if err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("empty ssh config path")
	}
	if isSSHConfigGlob(path) {
		if err := safepath.Under(sshRoot, filepath.Dir(path)); err != nil {
			return "", fmt.Errorf("ssh config glob %q: %w", path, err)
		}
		return path, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := safepath.Under(sshRoot, abs); err != nil {
		return "", fmt.Errorf("ssh config path %q: %w", path, err)
	}
	return abs, nil
}

func resolveSSHIncludePath(sshRoot, baseDir, inc string) (string, error) {
	inc = strings.TrimSpace(inc)
	inc = strings.Trim(inc, `"`)
	if inc == "" {
		return "", nil
	}
	inc, err := expandSSHPath(inc)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(inc) {
		inc = filepath.Join(baseDir, inc)
	}
	return sshConfigPathUnderRoot(sshRoot, inc)
}

func collectSSHConfigIncludes(sshRoot, path string, depth int) ([]string, error) {
	if depth > 32 {
		return nil, fmt.Errorf("ssh config Include depth > 32 at %s", path)
	}
	path, err := sshConfigPathUnderRoot(sshRoot, path)
	if err != nil {
		return nil, err
	}
	matches, err := filepath.Glob(path)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		if isSSHConfigGlob(path) {
			return nil, nil
		}
		st, statErr := safepath.Stat(path)
		if statErr == nil && !st.IsDir() {
			matches = []string{path}
		} else {
			return nil, nil
		}
	}
	var out []string
	seen := make(map[string]struct{})
	for _, m := range matches {
		m, err = sshConfigPathUnderRoot(sshRoot, m)
		if err != nil {
			return nil, err
		}
		st, statErr := safepath.Stat(m)
		if statErr != nil || st.IsDir() {
			continue
		}
		if _, dup := seen[m]; dup {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
		data, readErr := safepath.ReadFile(m)
		if readErr != nil {
			return nil, readErr
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, "\r"))
			im := includeLineRe.FindStringSubmatch(line)
			if im == nil {
				continue
			}
			incPath, incErr := resolveSSHIncludePath(sshRoot, filepath.Dir(m), im[1])
			if incErr != nil {
				return nil, incErr
			}
			if incPath == "" {
				continue
			}
			nested, nerr := collectSSHConfigIncludes(sshRoot, incPath, depth+1)
			if nerr != nil {
				return nil, nerr
			}
			for _, p := range nested {
				if _, dup := seen[p]; dup {
					continue
				}
				seen[p] = struct{}{}
				out = append(out, p)
			}
		}
	}
	return out, nil
}

func parseForwardsFromConfigFile(sshRoot, path, alias string) (ForwardSet, error) {
	if _, err := sshConfigPathUnderRoot(sshRoot, path); err != nil {
		return ForwardSet{}, err
	}
	data, err := safepath.ReadFile(path)
	if err != nil {
		return ForwardSet{}, err
	}
	var set ForwardSet
	var hostPatterns []string
	inMatch := false
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if matchLineRe.MatchString(line) {
			inMatch = true
			hostPatterns = nil
			continue
		}
		if hm := hostLineRe.FindStringSubmatch(line); hm != nil {
			inMatch = false
			hostPatterns = nil
			for _, p := range strings.Fields(strings.TrimSpace(hm[1])) {
				if !strings.EqualFold(p, "Match") {
					hostPatterns = append(hostPatterns, p)
				}
			}
			continue
		}
		if inMatch {
			continue
		}
		if !hostBlockMatches(hostPatterns, alias) {
			continue
		}
		m := forwardConfigLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		spec, perr := ParseForwardSpecLine(line)
		if perr != nil {
			continue
		}
		spec.Source = forwardSourceFallbackParser
		spec.FallbackWarn = true
		switch spec.Kind {
		case ForwardKindLocal:
			set.Local = append(set.Local, spec)
		case ForwardKindRemote:
			set.Remote = append(set.Remote, spec)
		case ForwardKindDynamic:
			set.Dynamic = append(set.Dynamic, spec)
		}
	}
	return set, nil
}

func hostBlockMatches(patterns []string, alias string) bool {
	if len(patterns) == 0 {
		return false
	}
	alias = strings.ToLower(strings.TrimSpace(alias))
	for _, p := range patterns {
		if hostPatternMatches(p, alias) {
			return true
		}
	}
	return false
}

func hostPatternMatches(pattern, alias string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	alias = strings.ToLower(strings.TrimSpace(alias))
	if pattern == "*" {
		return true
	}
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(pattern, "*") {
		return strings.Contains(alias, strings.Trim(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(alias, strings.TrimPrefix(pattern, "*"))
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(alias, strings.TrimSuffix(pattern, "*"))
	}
	if strings.HasPrefix(pattern, "!") {
		return !hostPatternMatches(strings.TrimPrefix(pattern, "!"), alias)
	}
	return pattern == alias
}
