package recordings

import (
	"encoding/base64"
	"fmt"
	"io"
	"time"

	"github.com/bytedance/sonic"
)

type castHeader struct {
	Version   int      `json:"version"`
	Term      castTerm `json:"term"`
	Timestamp int64    `json:"timestamp"`
	Title     string   `json:"title"`
}

type castTerm struct {
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
	Type string `json:"type"`
}

// ExportCast writes events as an asciinema v3 .cast file to w.
func ExportCast(events []Event, basename string, w io.Writer) error {
	cols, rows := 220, 50
	for _, e := range events {
		if e.Cols > 0 && e.Rows > 0 {
			cols, rows = e.Cols, e.Rows
			break
		}
	}

	hdr, _ := sonic.Marshal(castHeader{
		Version:   3,
		Term:      castTerm{Cols: cols, Rows: rows, Type: "xterm-256color"},
		Timestamp: parseFilenameTimestamp(basename),
		Title:     basename,
	})
	fmt.Fprintf(w, "%s\n", hdr)

	var prevMS int64
	if len(events) > 0 {
		prevMS = events[0].TimeMS
	}
	exitEmitted := false

	for _, e := range events {
		if exitEmitted {
			break
		}
		interval := float64(e.TimeMS-prevMS) / 1000.0
		if interval < 0 {
			interval = 0
		}

		switch {
		case e.Type == "resize":
			data, _ := sonic.Marshal(fmt.Sprintf("%dx%d", e.Cols, e.Rows))
			fmt.Fprintf(w, "[%.6f,\"r\",%s]\n", interval, data)
			prevMS = e.TimeMS

		case e.Type == "data" && (e.Direction == "stdout" || e.Direction == "stderr"):
			raw, err := base64.StdEncoding.DecodeString(e.DataB64)
			if err != nil {
				continue
			}
			data, _ := sonic.Marshal(string(raw))
			fmt.Fprintf(w, "[%.6f,\"o\",%s]\n", interval, data)
			prevMS = e.TimeMS

		case e.Type == "data" && e.Direction == "stdin":
			raw, err := base64.StdEncoding.DecodeString(e.DataB64)
			if err != nil {
				continue
			}
			data, _ := sonic.Marshal(string(raw))
			fmt.Fprintf(w, "[%.6f,\"i\",%s]\n", interval, data)
			prevMS = e.TimeMS

		case e.Type == "close":
			fmt.Fprintf(w, "[%.6f,\"x\",\"0\"]\n", interval)
			exitEmitted = true

		case e.Type == "error":
			fmt.Fprintf(w, "[%.6f,\"x\",\"1\"]\n", interval)
			exitEmitted = true
		}
	}
	return nil
}

// parseFilenameTimestamp extracts a Unix timestamp from a recording basename
// whose prefix is YYYYMMDD_HHMMSS (e.g. 20250528_120000_...).
func parseFilenameTimestamp(basename string) int64 {
	if len(basename) >= 15 {
		t, err := time.ParseInLocation("20060102_150405", basename[:15], time.Local)
		if err == nil {
			return t.Unix()
		}
	}
	return time.Now().Unix()
}
