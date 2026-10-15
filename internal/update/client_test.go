package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aead.dev/minisign"

	"github.com/prosperity-solutions/outfit/internal/testkit"
)

const plat = "linux_amd64"

func newClient(f *testkit.FakeRelease) *Client {
	return &Client{BaseURL: f.URL, APIURL: f.APIURL, Keys: f.Keys, GOOS: "linux", GOARCH: "amd64"}
}

func ver(t *testing.T, s string) *Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return &v
}

func TestFetchPublicLatestAndPinned(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Add("0.1.0")
	f.Add("0.2.0")
	c := newClient(f)

	got, err := c.Latest(context.Background())
	if err != nil || got.String() != "0.2.0" {
		t.Fatalf("Latest = %v, %v", got, err)
	}
	p, err := c.Fetch(context.Background(), nil)
	if err != nil || p.Version.String() != "0.2.0" || !bytes.Equal(p.Binary, testkit.Binary("0.2.0")) {
		t.Fatalf("Fetch latest = %+v, %v", p, err)
	}
	// Pinned, and a DOWNGRADE is allowed.
	p, err = c.Fetch(context.Background(), ver(t, "v0.1.0"))
	if err != nil || p.Version.String() != "0.1.0" || !bytes.Equal(p.Binary, testkit.Binary("0.1.0")) {
		t.Fatalf("Fetch 0.1.0 = %+v, %v", p, err)
	}
	if _, err := c.Fetch(context.Background(), ver(t, "9.9.9")); err == nil {
		t.Fatal("a version that does not exist was fetched")
	}
}

func TestFetchThroughTheAPIWithAToken(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Private, f.Token = true, "ghp_secrettoken123"
	f.Add("0.2.0")
	c := newClient(f)
	c.Token = Token{Value: "ghp_secrettoken123", Source: "GH_TOKEN"}

	p, err := c.Fetch(context.Background(), nil)
	if err != nil || p.Version.String() != "0.2.0" {
		t.Fatalf("Fetch = %+v, %v", p, err)
	}
	var sawAPI, sawStorage bool
	for _, r := range f.Requests() {
		switch {
		case strings.HasPrefix(r.Path, "/api/releases/assets/"):
			sawAPI = true
			if r.Accept != "application/octet-stream" || r.AuthWas != "Bearer ghp_secrettoken123" {
				t.Errorf("asset request headers: %+v", r)
			}
		case r.Redirect:
			sawStorage = true
			// The redirect leaves the host, and Go drops the Authorization header there.
			if r.HasAuth {
				t.Errorf("the token travelled to the storage host: %+v", r)
			}
		}
	}
	if !sawAPI || !sawStorage {
		t.Fatalf("expected asset requests through the API and storage: %+v", f.Requests())
	}
	// Latest through the API too.
	if v, err := c.Latest(context.Background()); err != nil || v.String() != "0.2.0" {
		t.Fatalf("Latest = %v, %v", v, err)
	}
}

func TestPrivateForkErrorsNameTheFix(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Private, f.Token = true, "ghp_right"
	f.Add("0.2.0")

	// No token: the public URLs answer 404.
	_, err := newClient(f).Fetch(context.Background(), nil)
	var ue *Error
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "HTTP 404") || strings.Contains(ue.Fix+ue.Msg, "gh auth") {
		t.Fatalf("no-token error = %#v", err)
	}
	// A wrong token: names where it came from, never its value.
	c := newClient(f)
	c.Token = Token{Value: "ghp_wrongtoken", Source: "OUTFIT_GITHUB_TOKEN"}
	_, err = c.Fetch(context.Background(), nil)
	if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "OUTFIT_GITHUB_TOKEN") || !strings.Contains(ue.Fix, "unset OUTFIT_GITHUB_TOKEN") || strings.Contains(ue.Msg+ue.Fix, "ghp_wrongtoken") {
		t.Fatalf("wrong-token error = %#v", err)
	}
}

func TestTokenIsNeverSentInTheClear(t *testing.T) {
	c := &Client{APIURL: "http://example.invalid/api", Token: Token{Value: "ghp_x", Source: "GH_TOKEN"}, Keys: testkit.NewFakeRelease(t, plat).Keys, GOOS: "linux", GOARCH: "amd64"}
	_, err := c.Fetch(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "refusing to send a GitHub token") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchRefusesWhatItCannotTrust(t *testing.T) {
	cases := map[string]struct {
		setup func(f *testkit.FakeRelease)
		want  string
	}{
		"bad checksum":      {func(f *testkit.FakeRelease) { f.Break.Checksum = true }, "SHA-256 mismatch"},
		"bad signature":     {func(f *testkit.FakeRelease) { f.Break.Signature = true }, "does not match any trusted"},
		"missing archive":   {func(f *testkit.FakeRelease) { f.Break.MissingFile = "outfit_0.2.0_" + plat + ".tar.gz" }, "HTTP 404"},
		"missing checksums": {func(f *testkit.FakeRelease) { f.Break.MissingFile = "checksums.txt" }, "HTTP 404"},
		"missing signature": {func(f *testkit.FakeRelease) { f.Break.MissingFile = "checksums.txt.minisig" }, "HTTP 404"},
		"symlink":           {func(f *testkit.FakeRelease) { f.Break.Symlink = true }, "not a plain file"},
		"path in name":      {func(f *testkit.FakeRelease) { f.Break.ExtraEntry = "../evil" }, "unexpected entry"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := testkit.NewFakeRelease(t, plat)
			c.setup(f)
			f.Add("0.2.0")
			_, err := newClient(f).Fetch(context.Background(), nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestFetchRefusesAnUnknownKeyAndNoKeys(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Add("0.2.0")
	other, _, _ := minisign.GenerateKey(rand.Reader)
	c := newClient(f)
	c.Keys = []minisign.PublicKey{other}
	if _, err := c.Fetch(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "nothing was installed") {
		t.Fatalf("unknown key: err = %v", err)
	}
	c.Keys = nil
	var ue *Error
	if _, err := c.Fetch(context.Background(), nil); !errors.As(err, &ue) || !strings.Contains(ue.Msg, "no trusted release keys") {
		t.Fatalf("no keys: err = %#v", err)
	}
}

func TestFetchRefusesAnOldBuildUnderANewTag(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Private, f.Token = true, "ghp_ok"
	f.Add("0.1.0")
	f.Add("0.2.0")
	f.Break.TagMismatch = true
	c := newClient(f)
	c.Token = Token{Value: "ghp_ok", Source: "OUTFIT_GITHUB_TOKEN"}
	if _, err := c.Fetch(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "not its own build") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchBoundsTime(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Add("0.2.0")
	c := newClient(f)
	c.MetaTimeout = time.Nanosecond
	if _, err := c.Latest(context.Background()); err == nil {
		t.Fatal("a 1ns timeout did not fail")
	}
}

func TestPlatform(t *testing.T) {
	for in, want := range map[string]string{"linux/amd64": "linux_amd64", "linux/arm64": "linux_arm64", "darwin/arm64": "darwin_all", "darwin/amd64": "darwin_all"} {
		goos, goarch, _ := strings.Cut(in, "/")
		if got, err := Platform(goos, goarch); err != nil || got != want {
			t.Errorf("Platform(%s) = %q, %v", in, got, err)
		}
	}
	if _, err := Platform("windows", "amd64"); err == nil {
		t.Error("windows has no build")
	}
}

func TestParseVersion(t *testing.T) {
	for _, ok := range []string{"0.1.0", "v1.20.3", " 2.0.0 "} {
		if _, err := ParseVersion(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"dev", "1.2", "1.2.3.4", "v1.2.x", "1.2.3-SNAPSHOT-abc", "01.2.3", "-1.0.0", ""} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	a, b := *ver(t, "0.9.0"), *ver(t, "0.10.0")
	if !a.Less(b) || b.Less(a) || a.Less(a) {
		t.Error("ordering is numeric, not textual")
	}
}

func TestFindTokenReadsOnlyOutfitGithubToken(t *testing.T) {
	env := map[string]string{"GITHUB_TOKEN": "c", "GH_TOKEN": "b"}
	get := func(k string) string { return env[k] }
	if tok, _ := FindToken(get); tok.Value != "" {
		t.Fatalf("an ambient GH_TOKEN or GITHUB_TOKEN must not be used: %+v", tok)
	}
	env["OUTFIT_GITHUB_TOKEN"] = " a "
	if tok, _ := FindToken(get); tok.Value != "a" || tok.Source != "OUTFIT_GITHUB_TOKEN" {
		t.Fatalf("tok = %+v", tok)
	}
	env["OUTFIT_GITHUB_TOKEN"] = "bad token\"quote"
	tok, ignored := FindToken(get)
	if tok.Value != "" || !strings.Contains(ignored, "OUTFIT_GITHUB_TOKEN") || strings.Contains(ignored, "quote") {
		t.Fatalf("tok = %+v ignored = %q", tok, ignored)
	}
}

func hostile(entries ...func(*tar.Writer)) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, e := range entries {
		e(tw)
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func file(name string, body string) func(*tar.Writer) {
	return func(tw *tar.Writer) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
}

func TestExtractBinaryAllowList(t *testing.T) {
	if b, err := ExtractBinary(hostile(file("LICENSE", "x"), file("outfit", "BIN"))); err != nil || string(b) != "BIN" {
		t.Fatalf("good archive: %q, %v", b, err)
	}
	// Forward compatible: a later release may add plain files (a man page, completions);
	// the frozen updater on every machine must still update. They are never extracted.
	if b, err := ExtractBinary(hostile(file("outfit", "BIN"), file("outfit.1", "man"), file("CHANGELOG.md", "c"), file("outfit.bash", "s"))); err != nil || string(b) != "BIN" {
		t.Fatalf("archive with extra plain files: %q, %v", b, err)
	}
	for name, a := range map[string][]byte{
		"path traversal": hostile(file("outfit", "x"), file("../outfit", "y")),
		"absolute":       hostile(file("/tmp/outfit", "x")),
		"subdirectory":   hostile(file("bin/outfit", "x")),
		"dot prefix":     hostile(file("./outfit", "x")),
		"hidden file":    hostile(file("outfit", "x"), file(".hidden", "y")),
		"space in name":  hostile(file("outfit", "x"), file("a b", "y")),
		"duplicate":      hostile(file("outfit", "x"), file("outfit", "y")),
		"no outfit":      hostile(file("LICENSE", "x")),
		"empty outfit":   hostile(file("outfit", "")),
		"directory": hostile(func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "outfit", Typeflag: tar.TypeDir, Mode: 0o755})
		}),
		"hardlink": hostile(func(tw *tar.Writer) {
			_ = tw.WriteHeader(&tar.Header{Name: "outfit", Typeflag: tar.TypeLink, Linkname: "LICENSE"})
		}),
		"not gzip": []byte("plain text"),
	} {
		if _, err := ExtractBinary(a); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestTokenGoesOnlyToTheRealAPIHostOrThisMachine(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://api.github.com/repos/o/r":      true,
		"http://127.0.0.1:8080/api":             true,
		"https://localhost:9/api":               true,
		"http://api.github.com/repos/o/r":       false, // not https
		"https://evil.example/api":              false,
		"https://api.github.com.evil.example/a": false,
		"http://127.0.0.1.evil.example/api":     false,
		"http://127.0.0.1@evil.example/api":     false,
		"https://api.github.com@evil.example/a": false,
		"http://localhost:80@evil.example/api":  false,
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := tokenHostOK(u); got != want {
			t.Errorf("tokenHostOK(%s) = %v, want %v", raw, got, want)
		}
	}
	// And the client enforces it: a token with an https API URL elsewhere is refused
	// before any request is made.
	c := &Client{APIURL: "https://evil.example/api", Token: Token{Value: "ghp_x", Source: "GH_TOKEN"}, Keys: testkit.NewFakeRelease(t, plat).Keys, GOOS: "linux", GOARCH: "amd64"}
	if _, err := c.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to send a GitHub token") {
		t.Fatalf("err = %v", err)
	}
}

func TestRedirectToPlainHTTPIsRefused(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Private, f.Token = true, "ghp_ok"
	f.Add("0.2.0")
	f.Break.InsecureRedirect = true
	c := newClient(f)
	c.Token = Token{Value: "ghp_ok", Source: "GH_TOKEN"}
	_, err := c.Fetch(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "refusing a redirect") {
		t.Fatalf("err = %v", err)
	}
}

func TestLatestVerifiedChecksTheSignatureButLatestDoesNot(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Break.Signature = true
	f.Add("0.2.0")
	c := newClient(f)
	if v, err := c.Latest(context.Background()); err != nil || v.String() != "0.2.0" {
		t.Fatalf("Latest (information only) = %v, %v", v, err)
	}
	if _, err := c.LatestVerified(context.Background()); err == nil {
		t.Fatal("LatestVerified believed an unsigned checksum list")
	}
}

func TestErrorsAreRetryableOnlyWhenTransient(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Keys: testkit.NewFakeRelease(t, plat).Keys, GOOS: "linux", GOARCH: "amd64"}
	_, err := c.Latest(context.Background())
	var ue *Error
	// Retryable, with no fix of its own (the CLI says which command to run again), and
	// the cause kept so a deadline can be told from a refusal.
	if !errors.As(err, &ue) || !ue.Retryable || ue.Cause == nil {
		t.Fatalf("connection refused: %#v", err)
	}
	f := testkit.NewFakeRelease(t, plat) // no releases: 404
	_, err = newClient(f).Latest(context.Background())
	if !errors.As(err, &ue) || ue.Retryable {
		t.Fatalf("404: %#v", err)
	}
}

func TestAnIdleDownloadIsCutOffButASlowSteadyOneIsNot(t *testing.T) {
	steady := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for i := 0; i < 5; i++ {
			_, _ = w.Write([]byte("0123456789"))
			fl.Flush()
			time.Sleep(60 * time.Millisecond)
		}
	}))
	defer steady.Close()
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-time.After(3 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer stalled.Close()
	// 5 chunks 60 ms apart take 300 ms: longer than the 150 ms idle limit overall, but never idle that long.
	c := &Client{IdleTimeout: 150 * time.Millisecond}
	if b, err := c.get(context.Background(), steady.URL, "", false, 10*time.Second, c.idleTimeout(), 1<<20); err != nil || len(b) != 50 {
		t.Fatalf("steady download: %d bytes, %v", len(b), err)
	}
	start := time.Now()
	_, err := c.get(context.Background(), stalled.URL, "", false, 10*time.Second, c.idleTimeout(), 1<<20)
	if err == nil || !IsTimeout(err) || time.Since(start) > 2*time.Second {
		t.Fatalf("stalled download: err=%v after %v", err, time.Since(start))
	}
}

func TestTokenLessArchiveIsFetchedFromTheVersionTheListNames(t *testing.T) {
	f := testkit.NewFakeRelease(t, plat)
	f.Add("0.1.0")
	f.Add("0.2.0")
	c := newClient(f)
	if _, err := c.Fetch(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	var archive string
	for _, r := range f.Requests() {
		if strings.HasSuffix(r.Path, ".tar.gz") {
			archive = r.Path
		}
	}
	if archive != "/releases/download/v0.2.0/outfit_0.2.0_"+plat+".tar.gz" {
		t.Fatalf("the archive came from %q", archive)
	}
}
