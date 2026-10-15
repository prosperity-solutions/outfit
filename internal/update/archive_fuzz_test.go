package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"testing"
)

func tarGz(entries []tar.Header, bodies [][]byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for i := range entries {
		h := entries[i]
		h.Size = int64(len(bodies[i]))
		_ = tw.WriteHeader(&h)
		_, _ = tw.Write(bodies[i])
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// FuzzExtractBinary (HANDOVER.md §7.5, the tar allow-list): whatever bytes arrive, it never
// panics, and when it returns a binary that binary is the content of exactly one plain
// entry named `outfit`, found again by an independent read of the same archive.
func FuzzExtractBinary(f *testing.F) {
	plain := tar.Header{Name: "outfit", Mode: 0o755, Typeflag: tar.TypeReg}
	f.Add(tarGz([]tar.Header{plain}, [][]byte{[]byte("#!/bin/sh\n")}))
	f.Add(tarGz([]tar.Header{plain, {Name: "LICENSE", Typeflag: tar.TypeReg}}, [][]byte{[]byte("x"), []byte("MIT")}))
	f.Add(tarGz([]tar.Header{{Name: "outfit", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, [][]byte{nil}))
	f.Add(tarGz([]tar.Header{plain, plain}, [][]byte{[]byte("a"), []byte("b")}))
	f.Add(tarGz([]tar.Header{{Name: "../outfit", Typeflag: tar.TypeReg}}, [][]byte{[]byte("x")}))
	f.Add(tarGz([]tar.Header{{Name: "dir/outfit", Typeflag: tar.TypeReg}}, [][]byte{[]byte("x")}))
	f.Add(tarGz([]tar.Header{{Name: ".hidden", Typeflag: tar.TypeReg}}, [][]byte{[]byte("x")}))
	f.Add([]byte("not gzip at all"))
	f.Fuzz(func(t *testing.T, archive []byte) {
		bin, err := ExtractBinary(archive)
		if err != nil {
			return
		}
		if len(bin) == 0 {
			t.Fatal("an empty binary was returned without an error")
		}
		zr, zerr := gzip.NewReader(bytes.NewReader(archive))
		if zerr != nil {
			t.Fatalf("accepted something that is not gzip: %v", zerr)
		}
		tr := tar.NewReader(zr)
		found := 0
		for {
			h, herr := tr.Next()
			if errors.Is(herr, io.EOF) {
				break
			}
			if herr != nil {
				t.Fatalf("accepted an archive an independent reader cannot read: %v", herr)
			}
			if h.Typeflag != tar.TypeReg {
				t.Fatalf("accepted an archive with a non-regular entry %q (type %v)", h.Name, h.Typeflag)
			}
			if h.Name == "outfit" {
				found++
				body, _ := io.ReadAll(tr)
				if !bytes.Equal(body, bin) {
					t.Fatal("the returned binary is not the content of the `outfit` entry")
				}
			}
		}
		if found != 1 {
			t.Fatalf("accepted an archive with %d `outfit` entries", found)
		}
	})
}
