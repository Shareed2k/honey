// Package main is the honey CLI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/shareed2k/honey/internal/cli"
	"github.com/shareed2k/honey/internal/transferagent"
)

// Set by goreleaser / -ldflags at link time.
var (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	raiseFDLimit() // avoid FD exhaustion on large parallel exec (macOS default is 256)
	cli.InitBuildInfo(version, commit, date)
	transferagent.SetEmbeddedHoneyVersion(version)
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
