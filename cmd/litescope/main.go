package main

import (
	"os"

	"github.com/croc100/litescope/internal/cli"
)

func main() {
	if err := cli.Root().Execute(); err != nil {
		os.Exit(1)
	}
}
