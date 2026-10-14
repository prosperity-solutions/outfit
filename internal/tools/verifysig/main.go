// Command verifysig checks a minisign signature against the public keys compiled
// into outfit (internal/update/keys.txt). The release workflow runs it on the
// freshly signed checksums.txt before anything is published, so a signing
// secret that does not match the committed key fails the release instead of
// shipping an update every client rejects.
//
//	go run ./internal/tools/verifysig checksums.txt checksums.txt.minisig [keys.txt]
//
// With a third argument the keys come from that file instead (the release job
// passes the previous release's keys.txt, so a signature the clients already
// installed would reject fails the build).
package main

import (
	"fmt"
	"os"

	"github.com/prosperity-solutions/outfit/internal/update"
)

func main() {
	if len(os.Args) != 3 && len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: verifysig <file> <file.minisig> [keys.txt]")
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
	keys, ids := update.Keys(), update.KeyIDs()
	if len(os.Args) == 4 {
		text, err := os.ReadFile(os.Args[3])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if keys, err = update.ParseKeys(string(text)); err != nil {
			fmt.Fprintln(os.Stderr, "verifysig:", os.Args[3]+":", err)
			os.Exit(2)
		}
		ids = nil
		for _, k := range keys {
			ids = append(ids, fmt.Sprintf("%016X", k.ID()))
		}
	}
	if err := update.Verify(keys, msg, sig); err != nil {
		fmt.Fprintln(os.Stderr, "verifysig:", err)
		os.Exit(1)
	}
	fmt.Println("signature OK (trusted keys:", ids, ")")
}
