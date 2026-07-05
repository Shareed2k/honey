// Package recordings parses session .hrec.jsonl files used by the web UI and TUI replay.
package recordings

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/jsonutil"
	"github.com/tidwall/gjson"
)

// Limits for loading a full recording into memory (aligned with HTTP API).
const (
	MaxPlayEvents = 10000000  // sanity limit for parsing
	MaxPlayBytes  = 128 << 20 // 128 MB limit for a single recording
)

// Event is one JSONL line in a .hrec.jsonl file.
type Event struct {
	TimeMS    int64           `json:"time_ms"`
	Type      string          `json:"type"`
	Direction string          `json:"direction,omitempty"`
	DataB64   string          `json:"data_b64,omitempty"`
	Cols      int             `json:"cols,omitempty"`
	Rows      int             `json:"rows,omitempty"`
	Message   string          `json:"message,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

// ParseOpenMessage extracts trigger, mode, provider, hostName, hostIP, user
// from the space-separated "key=value" pairs in an open event Message string.
func ParseOpenMessage(msg string) (trigger, mode, provider, hostName, hostIP, user string) {
	for _, part := range strings.Fields(strings.TrimSpace(msg)) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch k {
		case "trigger":
			trigger = v
		case "mode":
			mode = v
		case "provider":
			provider = v
		case "host":
			hostName = v
		case "ip":
			hostIP = v
		case "user":
			user = v
		}
	}
	return
}

// HasStructuredBatch matches web replay: batch/CUE logs use result or plan chunks.
func HasStructuredBatch(events []Event) bool {
	for _, e := range events {
		if e.Type == "result" {
			return true
		}
		if e.Type == "data" && e.Direction == "plan" {
			return true
		}
	}
	return false
}

// ValidateBaseName ensures the client cannot escape the record directory.
func ValidateBaseName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) || !strings.HasSuffix(name, ".hrec.jsonl") {
		return fmt.Errorf("invalid recording file name")
	}
	return nil
}

// ParseJSONL parses newline-delimited JSON events with MaxPlayEvents cap.
func ParseJSONL(raw []byte) ([]Event, error) {
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	// Increase max token size if there are very large lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, MaxPlayBytes)

	var events []Event
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var evt Event
		if err := jsonutil.Unmarshal(line, &evt); err != nil {
			return nil, fmt.Errorf("invalid recording event JSON: %w", err)
		}
		events = append(events, evt)
		if len(events) > MaxPlayEvents {
			return nil, fmt.Errorf("too many recording events (max %d)", MaxPlayEvents)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan recording events: %w", err)
	}
	return events, nil
}

// LoadEvents reads and parses a recording under recordDir using os.Root.
func LoadEvents(recordDir, baseName string) ([]Event, error) {
	if err := ValidateBaseName(baseName); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(recordDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	raw, err := root.ReadFile(baseName)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxPlayBytes {
		return nil, fmt.Errorf("recording file too large (max %d bytes)", MaxPlayBytes)
	}
	return ParseJSONL(raw)
}

// ReadOpenMessage returns the open event message line for metadata (list API).
func ReadOpenMessage(root *os.Root, name string) string {
	f, err := root.Open(name)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return ""
	}
	var evt Event
	if err := json.Unmarshal(sc.Bytes(), &evt); err != nil {
		return ""
	}
	if evt.Type != "open" {
		return ""
	}
	return evt.Message
}

type fileEntry struct {
	name string
	mod  int64
}

// ListHrecBasenames returns .hrec.jsonl basenames under recordDir, newest first.
func ListHrecBasenames(recordDir string) ([]string, error) {
	recordDir = strings.TrimSpace(recordDir)
	if recordDir == "" {
		return nil, fmt.Errorf("empty record dir")
	}
	root, err := os.OpenRoot(recordDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return nil, err
	}
	var files []fileEntry
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".hrec.jsonl") {
			continue
		}
		if strings.Contains(name, "/") || strings.Contains(name, `\`) {
			continue
		}
		info, err := de.Info()
		if err != nil {
			st, err2 := root.Stat(name)
			if err2 != nil {
				continue
			}
			files = append(files, fileEntry{name: name, mod: st.ModTime().UnixMilli()})
			continue
		}
		files = append(files, fileEntry{name: name, mod: info.ModTime().UnixMilli()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mod > files[j].mod
	})
	out := make([]string, len(files))
	for i := range files {
		out[i] = files[i].name
	}
	return out, nil
}

// ExtractFailedHosts parses a recording and returns unique failed host records.
func ExtractFailedHosts(events []Event) []hosts.Record {
	seen := make(map[string]bool)
	var failed []hosts.Record

	for _, e := range events {
		if e.Type == "result" {
			res := gjson.ParseBytes(e.Result)
			if !res.Get("success").Bool() {
				provider := res.Get("provider").String()
				name := res.Get("name").String()
				primaryIP := res.Get("primary_ip").String()

				key := provider + ":" + name + ":" + primaryIP
				if !seen[key] {
					seen[key] = true

					var extraIPs []string
					for _, ip := range res.Get("extra_ips").Array() {
						extraIPs = append(extraIPs, ip.String())
					}

					meta := make(map[string]string)
					for k, v := range res.Get("meta").Map() {
						meta[k] = v.String()
					}

					failed = append(failed, hosts.Record{
						Provider:  provider,
						Name:      name,
						PrimaryIP: primaryIP,
						ExtraIPs:  extraIPs,
						Meta:      meta,
					})
				}
			}
		}
	}
	return failed
}
