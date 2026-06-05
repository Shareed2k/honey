// Package anomaly provides log anomaly detection structures and algorithms.
//
// The standard demonstration templates and seed pool setups are compiled based on
// the evaluations from LLMLog (VLDB 2025) and CoLA (VLDB 2025).
// Licensed under Creative Commons Attribution-NonCommercial-NoDerivatives 4.0 International (CC BY-NC-ND 4.0).
package anomaly

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/jsonutil"
	"github.com/shareed2k/honey/internal/safepath"
)

// DemoInstance represents a labeled log template demonstration for LLMLog.
type DemoInstance struct {
	Source   string   // the source system name, e.g. "nginx", "postgres", "node"
	Tokens   []string // tokenized version of the template
	Template string   // the template string, e.g. "<*> - - [<*>] \"get /index.html http/1.1\" 200 <*>\""
	Anomaly  bool     // whether it is an anomaly
	Score    float64  // the anomaly score, 0.0-1.0
	Reason   string   // reason description
}

// PoolMu protects concurrent reading/writing of DefaultSeedPool.
var PoolMu sync.RWMutex

// DefaultSeedPool contains standard templates for Nginx, PostgreSQL, and Node.js.
var DefaultSeedPool = []DemoInstance{
	// Nginx - Normal
	{
		Source:   "nginx",
		Template: `<*> - - [<*>] "GET <*> HTTP/1.1" 200 <*>`,
		Anomaly:  false,
		Score:    0.0,
		Reason:   "routine HTTP GET request with 200 success response",
	},
	{
		Source:   "nginx",
		Template: `<*> - - [<*>] "POST <*> HTTP/1.1" 201 <*>`,
		Anomaly:  false,
		Score:    0.05,
		Reason:   "routine HTTP POST request with 201 success response",
	},
	// Nginx - Anomaly
	{
		Source:   "nginx",
		Template: `<*> - - [<*>] "GET <*> HTTP/1.1" 500 <*>`,
		Anomaly:  true,
		Score:    0.85,
		Reason:   "HTTP 500 internal server error indicating server-side application failure",
	},
	{
		Source:   "nginx",
		Template: `<*> - - [<*>] "GET /etc/passwd HTTP/1.1" 400 <*>`,
		Anomaly:  true,
		Score:    0.95,
		Reason:   "unauthorized directory traversal security probe attempting to read configuration",
	},

	// Postgres - Normal
	{
		Source:   "postgres",
		Template: `<*> [info] connection received: host=<*>`,
		Anomaly:  false,
		Score:    0.0,
		Reason:   "routine database connection received from a client",
	},
	{
		Source:   "postgres",
		Template: `<*> [info] statement: SELECT <*> FROM <*>`,
		Anomaly:  false,
		Score:    0.0,
		Reason:   "routine database SELECT query execution",
	},
	// Postgres - Anomaly
	{
		Source:   "postgres",
		Template: `<*> [error] password authentication failed for user <*>`,
		Anomaly:  true,
		Score:    0.90,
		Reason:   "failed database authentication attempt indicating potential brute force or misconfiguration",
	},
	{
		Source:   "postgres",
		Template: `<*> [fatal] remaining connection slots are reserved for non-replication superuser connections`,
		Anomaly:  true,
		Score:    0.95,
		Reason:   "database connection slots exhausted causing service disruption",
	},

	// Node.js - Normal
	{
		Source:   "node",
		Template: `info: server listening on port <*>`,
		Anomaly:  false,
		Score:    0.0,
		Reason:   "routine application server initialization log",
	},
	{
		Source:   "node",
		Template: `debug: query executed in <*> ms`,
		Anomaly:  false,
		Score:    0.0,
		Reason:   "routine database query execution latency debugging",
	},
	// Node.js - Anomaly
	{
		Source:   "node",
		Template: `error: uncaught exception: <*> at <*>`,
		Anomaly:  true,
		Score:    0.98,
		Reason:   "uncaught application exception leading to severe instability or crash",
	},
	{
		Source:   "node",
		Template: `warn: memory usage exceeded threshold: <*> mb`,
		Anomaly:  true,
		Score:    0.80,
		Reason:   "high memory consumption exceeding predefined safety limits",
	},
}

func init() {
	PoolMu.Lock()
	defer PoolMu.Unlock()
	for i := range DefaultSeedPool {
		DefaultSeedPool[i].Tokens = tokenize(DefaultSeedPool[i].Template)
	}
}

type feedbackJSON struct {
	Source  string  `json:"source"`
	Line    string  `json:"line"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
	Anomaly bool    `json:"anomaly"`
}

// LoadFeedbackDemos reads a JSONL feedback file, parses feedback records,
// and prepends them to DefaultSeedPool so they are preferred during demonstration selection.
func LoadFeedbackDemos(filePath string) error {
	if filePath == "" {
		return nil
	}

	data, err := safepath.ReadFile(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var newInstances []DemoInstance
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var rec feedbackJSON
		if err := jsonutil.Unmarshal([]byte(line), &rec); err != nil {
			return err
		}

		newInstances = append(newInstances, DemoInstance{
			Source:   rec.Source,
			Template: rec.Line,
			Anomaly:  rec.Anomaly,
			Score:    rec.Score,
			Reason:   rec.Reason,
			Tokens:   tokenize(rec.Line),
		})
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if len(newInstances) > 0 {
		PoolMu.Lock()
		DefaultSeedPool = append(newInstances, DefaultSeedPool...)
		PoolMu.Unlock()
	}

	return nil
}
