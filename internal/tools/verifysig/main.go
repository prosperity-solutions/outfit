// Command verifysig checks a minisign signature against the public keys compiled
// into outfit (internal/update/keys.txt). The release workflow runs it on the
// freshly signed checksums.txt before anything is published, so a signing
// secret that does not match the committed key fails the release instead of
// shipping an update every client rejects.
//
//	go run ./internal/tools/verifysig checksums.txt checksums.txt.minisig
package main

import (
	"fmt"
	"os"

	"github.com/prosperity-solutions/outfit/internal/update"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: verifysig <file> <file.minisig>")
		os.Exit(2)
	}
	msg, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	sig, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := update.VerifyEmbedded(msg, sig); err != nil {
		fmt.Fprintln(os.Stderr, "verifysig:", err)
		os.Exit(1)
	}
	fmt.Println("signature OK (trusted keys:", update.KeyIDs(), ")")
}
