package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
)

// allowed is every file a release archive may hold: plain files, plain names.
var allowed = map[string]bool{"outfit": true, "LICENSE": true, "README.md": true, "NOTICE": true}

// ExtractBinary returns the `outfit` executable from a release archive and refuses
// the archive when it holds anything else: another name, a directory, a link, a
// device, a second copy of a file, a path with a slash or "..". The archive is
// already signature- and checksum-checked; this is the belt to those braces.
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
		if !allowed[h.Name] {
			return nil, fmt.Errorf("unexpected entry %q", h.Name)
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
