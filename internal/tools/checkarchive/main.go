// Command checkarchive feeds release archives to the same allow-list `outfit update`
// applies (internal/update.ExtractBinary), so a change to .goreleaser.yaml that would
// make every real update fail ("unexpected entry ...") fails a pull request instead
// of the first release after it. scripts/ci/signing-smoke.sh runs it on a snapshot
// build.
//
//	go run ./internal/tools/checkarchive dist/outfit_*_linux_amd64.tar.gz ...
package main

import (
	"fmt"
	"os"

	"github.com/prosperity-solutions/outfit/internal/update"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: checkarchive <archive.tar.gz>...")
		os.Exit(2)
	}
	bad := 0
	for _, p := range os.Args[1:] {
		b, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		bin, err := update.ExtractBinary(b)
		if err != nil {
			fmt.Fprintf(os.Stderr, "checkarchive: %s: %v\n  (the archive layout in .goreleaser.yaml must match the allow-lists in internal/update/archive.go and install.sh)\n", p, err)
			bad++
			continue
		}
		fmt.Printf("ok: %s holds a plain %d byte outfit (the one file outfit update takes)\n", p, len(bin))
	}
	if bad > 0 {
		os.Exit(1)
	}
}
