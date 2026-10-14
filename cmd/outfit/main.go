// Command outfit runs a git monorepo under a profile: which hook checks run,
// and a few swapped files. See docs/HANDOVER.md for the spec.
package main

import (
	"os"

	"github.com/prosperity-solutions/outfit/internal/cli"
)

// Set by the release build through -ldflags; "dev" marks a local build.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Main(cli.Build{Version: version, Commit: commit, Date: date}))
}
