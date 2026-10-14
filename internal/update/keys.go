// Package update holds what outfit needs to trust a release: the public keys it
// was built to accept and the check of a minisign signature against them
// (HANDOVER.md §8). Self-update itself lands in the next change.
package update

import (
	_ "embed"
	"fmt"
	"strings"

	"aead.dev/minisign"
)

// keysFile is internal/update/keys.txt, compiled into the binary. The release
// workflow reads the same file (through internal/tools/verifysig) before it publishes.
//
//go:embed keys.txt
var keysFile string

// Keys parses the embedded keys. It panics on a malformed file: the file is
// source, and a test parses it, so a bad one never ships.
func Keys() []minisign.PublicKey {
	keys, err := ParseKeys(keysFile)
	if err != nil {
		panic("internal/update/keys.txt: " + err.Error())
	}
	return keys
}

// ParseKeys reads minisign public keys, one base64 line each. Blank lines,
// `untrusted comment:` lines and `#` comments are skipped. Several keys are
// allowed so a rotation can ship the new key in one release and switch the
// signing secret in the next (docs/release.md).
func ParseKeys(text string) ([]minisign.PublicKey, error) {
	var keys []minisign.PublicKey
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "untrusted comment:") {
			continue
		}
		var k minisign.PublicKey
		if err := k.UnmarshalText([]byte(line)); err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no public key found")
	}
	return keys, nil
}
