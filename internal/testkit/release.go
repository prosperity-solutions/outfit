package testkit

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"aead.dev/minisign"
)

// FakeRelease serves release files the way GitHub does, in both layouts install.sh
// and `outfit update` read: the public one (releases/latest/download/<file>,
// releases/download/v<ver>/<file>) and the API one (releases/latest, releases/tags/v<ver>,
// releases/assets/<id>, with a token). It signs checksums.txt with a throwaway
// minisign key and can break each part on purpose.
type FakeRelease struct {
	t *testing.T
	// URL is the public base (OUTFIT_BASE_URL); APIURL the API base (OUTFIT_API_URL).
	URL, APIURL string
	// StorageHost, when set before requests, makes asset downloads redirect to a
	// second server at that host name, as GitHub redirects to its storage.
	Keys    []minisign.PublicKey // the trusted public key(s) a test passes to the client
	priv    minisign.PrivateKey
	Private bool   // public URLs answer 404, the API needs Token
	Token   string // the token the API accepts when Private
	Plat    string // the platform being served, e.g. "linux_amd64"

	srv, storage *httptest.Server
	mu           sync.Mutex
	releases     map[string]*fakeVersion // "0.2.0" -> files
	latest       string
	reqs         []Request
	// Break holds the tampering switches.
	Break struct {
		Checksum    bool   // archive bytes differ from what checksums.txt says
		Signature   bool   // signature made by a different key
		MissingFile string // 404 for this file name
		TagMismatch bool   // API: tag_name says latest but the files are an older build
		ExtraEntry  string // archive also holds a file with this name
		Symlink     bool   // archive holds a symlink named outfit
	}
}

// Request is one request the fake saw.
type Request struct {
	Path     string
	Host     string
	HasAuth  bool
	AuthWas  string
	Accept   string
	Redirect bool
}

type fakeVersion struct {
	files map[string][]byte // name -> content
	ids   map[string]int
}

// NewFakeRelease starts the server. Add releases with Add, then point a client at it.
func NewFakeRelease(t *testing.T, plat string) *FakeRelease {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &FakeRelease{t: t, Keys: []minisign.PublicKey{pub}, priv: priv, Plat: plat, releases: map[string]*fakeVersion{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	f.storage = httptest.NewServer(http.HandlerFunc(f.handleStorage))
	t.Cleanup(f.srv.Close)
	t.Cleanup(f.storage.Close)
	f.URL = f.srv.URL
	f.APIURL = f.srv.URL + "/api"
	return f
}

// Binary is the executable content a release of this version carries.
func Binary(version string) []byte {
	return []byte("#!/bin/sh\necho fake outfit " + version + "\n")
}

// Add publishes version (e.g. "0.2.0") and makes it the latest when it is the highest so far.
func (f *FakeRelease) Add(version string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.add(version, Binary(version))
}

func (f *FakeRelease) add(version string, bin []byte) {
	name := fmt.Sprintf("outfit_%s_%s.tar.gz", version, f.Plat)
	archive := f.tarball(bin)
	sum := sha256.Sum256(archive)
	if f.Break.Checksum {
		sum = sha256.Sum256([]byte("something else"))
	}
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	signer := f.priv
	if f.Break.Signature {
		_, other, _ := minisign.GenerateKey(rand.Reader)
		signer = other
	}
	v := &fakeVersion{files: map[string][]byte{
		name:                    archive,
		"checksums.txt":         sums,
		"checksums.txt.minisig": minisign.Sign(signer, sums),
	}, ids: map[string]int{}}
	names := make([]string, 0, len(v.files))
	for n := range v.files {
		names = append(names, n)
	}
	sort.Strings(names)
	for i, n := range names {
		v.ids[n] = 1000*len(f.releases) + 100 + i
	}
	f.releases[version] = v
	if f.latest == "" || versionLess(f.latest, version) {
		f.latest = version
	}
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

func (f *FakeRelease) tarball(bin []byte) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	put := func(name string, mode int64, body []byte) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(body)
	}
	if f.Break.Symlink {
		_ = tw.WriteHeader(&tar.Header{Name: "outfit", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd", Mode: 0o777})
	} else {
		put("outfit", 0o755, bin)
	}
	put("LICENSE", 0o644, []byte("MIT"))
	put("README.md", 0o644, []byte("readme"))
	put("NOTICE", 0o644, []byte("notice"))
	if f.Break.ExtraEntry != "" {
		put(f.Break.ExtraEntry, 0o644, []byte("evil"))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

// Requests returns what the fake has seen so far.
func (f *FakeRelease) Requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Request(nil), f.reqs...)
}

func (f *FakeRelease) record(r *http.Request, redirect bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, Request{
		Path: r.URL.Path, Host: r.Host, HasAuth: r.Header.Get("Authorization") != "",
		AuthWas: r.Header.Get("Authorization"), Accept: r.Header.Get("Accept"), Redirect: redirect,
	})
}

func (f *FakeRelease) handleStorage(w http.ResponseWriter, r *http.Request) {
	f.record(r, true)
	// /<version>/<file>
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 2)
	f.serveFile(w, parts[0], parts[1])
}

func (f *FakeRelease) serveFile(w http.ResponseWriter, version, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.releases[version]
	if v == nil || name == f.Break.MissingFile {
		http.NotFound(w, nil)
		return
	}
	body, ok := v.files[name]
	if !ok {
		http.NotFound(w, nil)
		return
	}
	_, _ = w.Write(body)
}

func (f *FakeRelease) handle(w http.ResponseWriter, r *http.Request) {
	f.record(r, false)
	p := r.URL.Path
	switch {
	case strings.HasPrefix(p, "/api/"):
		f.handleAPI(w, r, strings.TrimPrefix(p, "/api"))
	case f.Private:
		http.NotFound(w, r)
	case strings.HasPrefix(p, "/releases/latest/download/"):
		f.serveFile(w, f.latestVersion(), strings.TrimPrefix(p, "/releases/latest/download/"))
	case strings.HasPrefix(p, "/releases/download/v"):
		rest := strings.TrimPrefix(p, "/releases/download/v")
		parts := strings.SplitN(rest, "/", 2)
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		f.serveFile(w, parts[0], parts[1])
	default:
		http.NotFound(w, r)
	}
}

func (f *FakeRelease) latestVersion() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latest
}

func (f *FakeRelease) handleAPI(w http.ResponseWriter, r *http.Request, p string) {
	if f.Private && r.Header.Get("Authorization") != "Bearer "+f.Token {
		http.NotFound(w, r) // GitHub answers 404 to an unauthorised read of a private repo
		return
	}
	switch {
	case p == "/releases/latest" || strings.HasPrefix(p, "/releases/tags/v"):
		version := f.latestVersion()
		if strings.HasPrefix(p, "/releases/tags/v") {
			version = strings.TrimPrefix(p, "/releases/tags/v")
		}
		f.mu.Lock()
		v := f.releases[version]
		tag := "v" + version
		files := v
		if f.Break.TagMismatch && p == "/releases/latest" {
			// Claim the latest version but serve an older release's files.
			tag = "v" + f.latest
			for ver, rel := range f.releases {
				if ver != f.latest {
					files = rel
				}
			}
		}
		f.mu.Unlock()
		if v == nil {
			http.NotFound(w, r)
			return
		}
		var assets []string
		names := make([]string, 0, len(files.ids))
		for n := range files.ids {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if n == f.Break.MissingFile {
				continue
			}
			assets = append(assets, fmt.Sprintf(`{"url":"%s/api/releases/assets/%d","id":%d,"name":%q}`, f.URL, files.ids[n], files.ids[n], n))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"tag_name":%q,"assets":[%s]}`, tag, strings.Join(assets, ","))
	case strings.HasPrefix(p, "/releases/assets/"):
		id, _ := strconv.Atoi(strings.TrimPrefix(p, "/releases/assets/"))
		f.mu.Lock()
		var version, name string
		for ver, rel := range f.releases {
			for n, i := range rel.ids {
				if i == id {
					version, name = ver, n
				}
			}
		}
		f.mu.Unlock()
		if version == "" {
			http.NotFound(w, r)
			return
		}
		// Like GitHub: the asset endpoint redirects to storage on another host.
		storage := strings.Replace(f.storage.URL, "127.0.0.1", "localhost", 1)
		http.Redirect(w, r, fmt.Sprintf("%s/%s/%s", storage, version, name), http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}
