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
	cli.InitBuildInfo(version, commit, date)
	transferagent.SetEmbeddedHoneyVersion(version)
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
