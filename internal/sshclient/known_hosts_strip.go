// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found at https://cs.opensource.google/go/x/crypto/+/master:LICENSE
//
// Portions below are adapted from golang.org/x/crypto/ssh/knownhosts (host pattern
// parsing and matching) so we can drop stale entries without invoking ssh-keygen.

package sshclient

import (
	"bufio"
	"bytes"
	"crypto/hmac"

	// #nosec G505 -- OpenSSH known_hosts hashed host format (HMAC-SHA1); interop with golang.org/x/crypto/ssh/knownhosts.
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/safepath"

	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	khMarkerCert    = "@cert-authority"
	khMarkerRevoked = "@revoked"
	khSHA1HashType  = "1"
)

// khAddr mirrors knownhosts.addr (host + port) for OpenSSH known_hosts matching.
type khAddr struct {
	host, port string
}

func (a *khAddr) stringForKnownHosts() string {
	h := a.host
	if strings.Contains(h, ":") {
		h = "[" + h + "]"
	}
	return h + ":" + a.port
}

func dialKhAddrs(hostname string, remote net.Addr) ([]khAddr, error) {
	if remote == nil {
		return nil, fmt.Errorf("nil remote address")
	}
	rh, rp, err := net.SplitHostPort(remote.String())
	if err != nil {
		return nil, fmt.Errorf("remote addr %q: %w", remote.String(), err)
	}
	seen := make(map[string]struct{})
	var out []khAddr
	add := func(a khAddr) {
		k := a.host + "\x00" + a.port
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, a)
	}
	add(khAddr{host: rh, port: rp})

	h := strings.TrimSpace(hostname)
	if h == "" {
		return out, nil
	}
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		add(khAddr{host: h, port: "22"})
		return out, nil
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
	}
	add(khAddr{host: host, port: port})
	return out, nil
}

type khMatcher interface {
	match(a khAddr) bool
}

type khHostPattern struct {
	negate bool
	addr   khAddr
}

type khHostPatterns []khHostPattern

func (ps khHostPatterns) match(a khAddr) bool {
	matched := false
	for _, p := range ps {
		if !p.match(a) {
			continue
		}
		if p.negate {
			return false
		}
		matched = true
	}
	return matched
}

func wildcardMatch(pat, str []byte) bool {
	for {
		if len(pat) == 0 {
			return len(str) == 0
		}
		if len(str) == 0 {
			return false
		}
		if pat[0] == '*' {
			if len(pat) == 1 {
				return true
			}
			for j := range str {
				if wildcardMatch(pat[1:], str[j:]) {
					return true
				}
			}
			return false
		}
		if pat[0] == '?' || pat[0] == str[0] {
			pat = pat[1:]
			str = str[1:]
		} else {
			return false
		}
	}
}

func (p *khHostPattern) match(a khAddr) bool {
	return wildcardMatch([]byte(p.addr.host), []byte(a.host)) && p.addr.port == a.port
}

func khNextWord(line []byte) (string, []byte) {
	i := bytes.IndexAny(line, "\t ")
	if i == -1 {
		return string(line), nil
	}
	return string(line[:i]), bytes.TrimSpace(line[i:])
}

func khParseLine(line []byte) (marker, hostField string, err error) {
	if w, next := khNextWord(line); w == khMarkerCert || w == khMarkerRevoked {
		marker = w
		line = next
	}
	hostField, line = khNextWord(line)
	if len(line) == 0 {
		return "", "", errors.New("missing host pattern")
	}
	_, line = khNextWord(line)
	if len(line) == 0 {
		return "", "", errors.New("missing key type")
	}
	keyBlob, _ := khNextWord(line)
	if _, err := base64.StdEncoding.DecodeString(keyBlob); err != nil {
		return "", "", err
	}
	return marker, hostField, nil
}

func newKhHostnameMatcher(pattern string) (khMatcher, error) {
	parts := strings.Split(pattern, ",")
	hps := make(khHostPatterns, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		var a khAddr
		var negate bool
		if p[0] == '!' {
			negate = true
			p = p[1:]
		}
		if len(p) == 0 {
			return nil, errors.New("negation without hostname")
		}
		var err error
		if p[0] == '[' {
			a.host, a.port, err = net.SplitHostPort(p)
			if err != nil {
				return nil, err
			}
		} else {
			a.host, a.port, err = net.SplitHostPort(p)
			if err != nil {
				a.host = p
				a.port = "22"
			}
		}
		hps = append(hps, khHostPattern{negate: negate, addr: a})
	}
	return hps, nil
}

func khDecodeHash(encoded string) (hashType string, salt, hash []byte, err error) {
	if len(encoded) == 0 || encoded[0] != '|' {
		return "", nil, nil, errors.New("hashed host must start with '|'")
	}
	components := strings.Split(encoded, "|")
	if len(components) != 4 {
		return "", nil, nil, fmt.Errorf("got %d hash components, want 3", len(components)-1)
	}
	hashType = components[1]
	if salt, err = base64.StdEncoding.DecodeString(components[2]); err != nil {
		return "", nil, nil, err
	}
	if hash, err = base64.StdEncoding.DecodeString(components[3]); err != nil {
		return "", nil, nil, err
	}
	return hashType, salt, hash, nil
}

type khHashedHost struct {
	salt, hash []byte
}

func newKhHashedHost(encoded string) (*khHashedHost, error) {
	typ, salt, hash, err := khDecodeHash(encoded)
	if err != nil {
		return nil, err
	}
	if typ != khSHA1HashType {
		return nil, fmt.Errorf("hash type %q, want %q", typ, khSHA1HashType)
	}
	return &khHashedHost{salt: salt, hash: hash}, nil
}

func khHashHost(hostname string, salt []byte) []byte {
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(hostname))
	return mac.Sum(nil)
}

func (h *khHashedHost) match(a khAddr) bool {
	return bytes.Equal(khHashHost(knownhosts.Normalize(a.stringForKnownHosts()), h.salt), h.hash)
}

func khMatcherFromHostField(hostField string) (khMatcher, error) {
	if len(hostField) == 0 {
		return nil, errors.New("empty host field")
	}
	if hostField[0] == '|' {
		return newKhHashedHost(hostField)
	}
	return newKhHostnameMatcher(hostField)
}

func knownHostsLineMatchesAddrs(line []byte, addrs []khAddr) (bool, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] == '#' {
		return false, nil
	}
	_, hostField, err := khParseLine(line)
	if err != nil {
		return false, err
	}
	m, err := khMatcherFromHostField(hostField)
	if err != nil {
		return false, err
	}
	for _, a := range addrs {
		if m.match(a) {
			return true, nil
		}
	}
	return false, nil
}

// rewriteKnownHostsStrippingAddrs removes every line whose host patterns match any of addrs
// (same idea as ssh-keygen -R). Malformed lines are kept. Returns the number of lines removed.
func rewriteKnownHostsStrippingAddrs(path string, addrs []khAddr) (removed int, err error) {
	data, err := safepath.ReadFile(path)
	if err != nil {
		return 0, err
	}
	var out bytes.Buffer
	scan := bufio.NewScanner(bytes.NewReader(data))
	// Allow long lines (some known_hosts can be huge single-line certs in theory).
	const maxKnownHostsScanToken = 10 * 1024 * 1024
	scan.Buffer(make([]byte, 0, 64*1024), maxKnownHostsScanToken)
	for scan.Scan() {
		raw := scan.Bytes()
		if len(raw) == 0 {
			out.WriteByte('\n')
			continue
		}
		match, perr := knownHostsLineMatchesAddrs(raw, addrs)
		if perr != nil {
			out.Write(raw)
			out.WriteByte('\n')
			continue
		}
		if match {
			removed++
			continue
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	if err := scan.Err(); err != nil {
		return 0, err
	}
	if removed == 0 {
		return 0, nil
	}
	mode := os.FileMode(0o600)
	if st, statErr := safepath.Stat(path); statErr == nil {
		mode = st.Mode() & 0o777
	}
	if err := safepath.WriteFile(path, out.Bytes(), mode); err != nil {
		return 0, err
	}
	return removed, nil
}
