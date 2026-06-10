package main

import (
	"os"

	"github.com/croc100/litescope/internal/cli"
)

// Injected at build time by goreleaser via -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	root := cli.Root()
	root.Version = version + " (" + commit[:min(7, len(commit))] + ") " + date
	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
