package main

import (
	"fmt"
	"os"

	"honey/internal/cli"
)

// Set by goreleaser / -ldflags at link time.
var (
	version = "0.0.0-dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	cli.InitBuildInfo(version, commit, date)
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
