package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"regexp"
)

// plainName is every name a release archive entry may have: one path component of
// ordinary characters. No slash, no "..", no dot-prefix, nothing a shell or a
// filesystem treats specially.
var plainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// ExtractBinary returns the `outfit` executable from a release archive. It is the
// only file outfit ever takes out of it, and it is read into memory, never written
// to a path the archive names. Everything else in the archive is checked but ignored:
// other PLAIN FILES with plain names are skipped, so a later release can add a man
// page or shell completions without every installed outfit refusing to update (the
// binary that reads archives is frozen on each machine; the archive is not). What is
// refused: anything that is not a regular file (a link, a directory, a device), a
// name with a path in it, a second `outfit`, a missing or empty `outfit`, and
// oversize entries. The archive is already signature- and checksum-checked; this is
// the belt to those braces. install.sh is stricter (it is fetched fresh, so it can
// change in step with .goreleaser.yaml); tests/validate-install-contract.sh keeps
// the two compatible and scripts/ci/signing-smoke.sh feeds a real snapshot archive
// through here on every PR.
func ExtractBinary(archive []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive: %w", err)
	}
	// A bomb guard: nothing legitimate unpacks to more than the binary limit.
	tr := tar.NewReader(io.LimitReader(zr, maxBinary+(8<<20)))
	var bin []byte
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("unreadable archive: %w", err)
		}
		if !plainName.MatchString(h.Name) {
			return nil, fmt.Errorf("unexpected entry %q (not a plain file name)", h.Name)
		}
		if h.Typeflag != tar.TypeReg {
			return nil, fmt.Errorf("entry %q is not a plain file", h.Name)
		}
		if seen[h.Name] {
			return nil, fmt.Errorf("entry %q appears twice", h.Name)
		}
		seen[h.Name] = true
		if h.Size > maxBinary {
			return nil, fmt.Errorf("entry %q is larger than %d bytes", h.Name, maxBinary)
		}
		if h.Name != "outfit" {
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return nil, fmt.Errorf("unreadable archive: %w", err)
			}
			continue
		}
		bin, err = io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("unreadable archive: %w", err)
		}
	}
	if len(bin) == 0 {
		return nil, errors.New("no outfit binary inside")
	}
	return bin, nil
}
