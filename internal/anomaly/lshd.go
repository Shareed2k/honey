// Package anomaly provides anomaly detection algorithms based on LSH (Locality Sensitive Hashing)
// and other clustering techniques.
//
// LogLSHD (Locality-Sensitive Hashing with Sequence-Alignment Clustering)
// is based on the algorithm and research proposed in:
// "RT-LogAAS: A Real-Time Log Anomaly Analysis System for Net-Cloud"
// Licensed under Creative Commons Attribution 4.0 International (CC BY 4.0).
// For license details, see: https://creativecommons.org/licenses/by/4.0/
package anomaly

import (
	"context"
	"hash/fnv"
	"math"
	"math/bits"
	"regexp"
	"strings"
	"sync"
)

// LSHDDetector defines the interface for LSHD anomaly detection.
type LSHDDetector interface {
	Detector
	Template(line string) (string, error)
}

// lshdDetector implements LSHDDetector using Locality Sensitive Hashing.
type lshdDetector struct {
	mu       sync.Mutex
	clusters map[int]*Cluster
	// bands stores the hash buckets for LSH bands (4 bands, 16 bits each).
	bands   [4]map[uint16][]int
	N       int
	tokenDF map[string]int
}

// Cluster represents a log cluster identified by the anomaly detector.
type Cluster struct {
	// ID is the unique identifier for the cluster.
	ID int
	// Template is the log template representing this cluster.
	Template []string
	// SimHash is the similarity hash for the template.
	SimHash uint64
	// Count is the number of logs assigned to this cluster.
	Count int
}

// NewLSHDDetector creates a new LSHDDetector and initializes its internal data structures.
func NewLSHDDetector() LSHDDetector {
	d := &lshdDetector{
		clusters: make(map[int]*Cluster),
		tokenDF:  make(map[string]int),
		N:        0,
	}
	for i := range d.bands {
		d.bands[i] = make(map[uint16][]int)
	}
	return d
}

var _ LSHDDetector = (*lshdDetector)(nil)

var tokenRegexp = regexp.MustCompile(`[a-zA-Z0-9_\-%]+|[^a-zA-Z0-9_\-%\s]`)

// tokenize splits a line into alphanumeric words (including _, -, %) and individual punctuation marks.
func tokenize(line string) []string {
	matches := tokenRegexp.FindAllString(line, -1)
	tokens := make([]string, len(matches))
	for i, m := range matches {
		tokens[i] = strings.ToLower(m)
	}
	return tokens
}

// computeWeight calculates the TF-IDF-like weight of a token.
func (d *lshdDetector) computeWeight(token string) float64 {
	df := d.tokenDF[token]
	return math.Log(float64(d.N+1)/float64(df+1)) + 1.0
}

// computeSimHash calculates a 64-bit SimHash fingerprint for the given tokens.
func (d *lshdDetector) computeSimHash(tokens []string) uint64 {
	var v [64]float64

	for _, t := range tokens {
		weight := d.computeWeight(t)
		h := fnv.New64a()
		h.Write([]byte(t))
		hVal := h.Sum64()

		for i := 0; i < 64; i++ {
			bit := (hVal >> i) & 1
			if bit == 1 {
				v[i] += weight
			} else {
				v[i] -= weight
			}
		}
	}

	var simhash uint64
	for i := 0; i < 64; i++ {
		if v[i] >= 0 {
			simhash |= (uint64(1) << i)
		}
	}

	return simhash
}

// Score computes an anomaly score for a given log line.
func (d *lshdDetector) Score(_ context.Context, line string) (Result, error) {
	template, err := d.Template(line)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Score:    0,
		Anomaly:  false,
		Reason:   "lshd:" + template,
		Original: line,
	}, nil
}

// Template extracts/returns the template for a given log line.
func (d *lshdDetector) Template(line string) (string, error) {
	tokens := tokenize(line)

	d.mu.Lock()
	defer d.mu.Unlock()

	uniqueTokens := make(map[string]bool)
	for _, tok := range tokens {
		uniqueTokens[tok] = true
	}

	d.N++
	for tok := range uniqueTokens {
		d.tokenDF[tok]++
	}

	currentSimHash := d.computeSimHash(tokens)

	bandKeys := [4]uint16{
		uint16(currentSimHash & 0xFFFF),
		uint16((currentSimHash >> 16) & 0xFFFF),
		uint16((currentSimHash >> 32) & 0xFFFF),
		uint16((currentSimHash >> 48) & 0xFFFF),
	}

	candidatesSet := make(map[int]bool)
	for i, key := range bandKeys {
		for _, id := range d.bands[i][key] {
			candidatesSet[id] = true
		}
	}

	var bestCluster *Cluster
	maxLCSSim := -1.0

	for id := range candidatesSet {
		cluster := d.clusters[id]
		dist := bits.OnesCount64(cluster.SimHash ^ currentSimHash)
		if dist <= 8 {
			lcsLen := lcsLength(tokens, cluster.Template)
			var lcsSim float64
			if len(tokens) == 0 && len(cluster.Template) == 0 {
				lcsSim = 1.0
			} else {
				lcsSim = 2.0 * float64(lcsLen) / float64(len(tokens)+len(cluster.Template))
			}
			if lcsSim >= 0.75 {
				if lcsSim > maxLCSSim {
					maxLCSSim = lcsSim
					bestCluster = cluster
				}
			}
		}
	}

	if bestCluster != nil {
		bestCluster.Count++
		bestCluster.Template = alignTemplates(bestCluster.Template, tokens)
		return strings.Join(bestCluster.Template, " "), nil
	}

	newID := len(d.clusters) + 1
	newCluster := &Cluster{
		ID:       newID,
		Template: tokens,
		SimHash:  currentSimHash,
		Count:    1,
	}
	d.clusters[newID] = newCluster
	for i, key := range bandKeys {
		d.bands[i][key] = append(d.bands[i][key], newID)
	}

	return strings.Join(tokens, " "), nil
}

// alignTemplates aligns two template/token sequences, replacing differences with "<*>" and collapsing consecutive wildcards.
func alignTemplates(temp []string, tokens []string) []string {
	m := len(temp)
	n := len(tokens)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if temp[i-1] == tokens[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}

	var aligned []string
	i, j := m, n
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && temp[i-1] == tokens[j-1]:
			aligned = append(aligned, temp[i-1])
			i--
			j--
		case j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]):
			aligned = append(aligned, "<*>")
			j--
		default:
			aligned = append(aligned, "<*>")
			i--
		}
	}

	// Reverse the aligned list
	for l, r := 0, len(aligned)-1; l < r; l, r = l+1, r-1 {
		aligned[l], aligned[r] = aligned[r], aligned[l]
	}

	// Collapse consecutive "<*>"
	var collapsed []string
	for _, tok := range aligned {
		if tok == "<*>" {
			if len(collapsed) > 0 && collapsed[len(collapsed)-1] == "<*>" {
				continue
			}
		}
		collapsed = append(collapsed, tok)
	}

	return collapsed
}

// lcsLength computes the length of the Longest Common Subsequence of two token sequences.
func lcsLength(a, b []string) int {
	m := len(a)
	n := len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if a[i-1] == b[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			}
		}
	}
	return dp[m][n]
}
