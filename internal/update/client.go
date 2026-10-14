package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"aead.dev/minisign"
)

// Error is a failure with the exact fix to run, which is what agents and people
// follow. The CLI turns it into its own error type.
type Error struct {
	Msg string
	Fix string
}

func (e *Error) Error() string { return e.Msg }

func fixf(fix, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Fix: fix}
}

// Client fetches, verifies and unpacks a release. One attempt per request, no
// retry loops (HANDOVER.md §5); a failed check just tries again next time.
type Client struct {
	BaseURL string // public download base; for tests, OUTFIT_BASE_URL
	APIURL  string // API base; for tests, OUTFIT_API_URL
	Token   Token
	HTTP    *http.Client
	Keys    []minisign.PublicKey
	GOOS    string
	GOARCH  string
	// MetaTimeout bounds each small request (release metadata, checksums, signature),
	// DownloadTimeout the archive. Zero means 5 s and 60 s.
	MetaTimeout     time.Duration
	DownloadTimeout time.Duration
}

// Package is a verified release, ready to install.
type Package struct {
	Version Version
	Archive string
	Binary  []byte // the `outfit` executable from the archive
}

// Size limits: a release file is a few MB; anything much bigger is not ours.
const (
	maxMeta    = 1 << 20   // JSON and text files
	maxArchive = 64 << 20  // compressed archive
	maxBinary  = 128 << 20 // the executable once unpacked
)

// Platform names the release build for an OS/arch, as install.sh does.
func Platform(goos, goarch string) (string, error) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return "linux_amd64", nil
	case "linux/arm64":
		return "linux_arm64", nil
	case "darwin/amd64", "darwin/arm64":
		return "darwin_all", nil
	}
	return "", fixf("", "there is no outfit build for %s/%s (there are linux amd64/arm64 and macOS builds)", goos, goarch)
}

func (c *Client) base() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) api() string {
	if c.APIURL != "" {
		return strings.TrimRight(c.APIURL, "/")
	}
	return DefaultAPIURL
}

func (c *Client) http() *http.Client {
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	cp := *hc
	// Never follow a redirect to plain http (except to this machine, for tests):
	// the token must not travel in the clear. Go itself drops the Authorization
	// header when a redirect leaves the host, so the storage redirect of an asset
	// download does not carry it.
	cp.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if !secureEnough(req.URL) {
			return fmt.Errorf("refusing a redirect to %s over plain http", req.URL.Host)
		}
		return nil
	}
	return &cp
}

// secureEnough reports whether a token may be sent to u: https, or http to this machine.
func secureEnough(u *url.URL) bool {
	if u.Scheme == "https" {
		return true
	}
	h := u.Hostname()
	return u.Scheme == "http" && (h == "localhost" || h == "127.0.0.1" || h == "::1")
}

func (c *Client) metaTimeout() time.Duration {
	if c.MetaTimeout > 0 {
		return c.MetaTimeout
	}
	return 5 * time.Second
}

func (c *Client) downloadTimeout() time.Duration {
	if c.DownloadTimeout > 0 {
		return c.DownloadTimeout
	}
	return 60 * time.Second
}

// get fetches one URL, bounded in time and size. auth sends the token, and
// refuses to when the URL is not secure.
func (c *Client) get(ctx context.Context, rawURL, accept string, auth bool, timeout time.Duration, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if auth && !secureEnough(u) {
		return nil, fixf("", "refusing to send a GitHub token to %s (only https; OUTFIT_API_URL is for tests)", u.Host)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "outfit-update")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if auth {
		req.Header.Set("Authorization", "Bearer "+c.Token.Value)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, fixf("", "could not reach %s: %v", u.Host, scrub(err, c.Token.Value))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError(u, resp.StatusCode, auth)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fixf("", "could not read from %s: %v", u.Host, scrub(err, c.Token.Value))
	}
	if int64(len(b)) > limit {
		return nil, fixf("", "%s sent more than %d bytes; not an outfit release file", u.Path, limit)
	}
	return b, nil
}

// scrub keeps a token out of an error message, however it got in.
func scrub(err error, token string) error {
	if token == "" {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), token, "***"))
}

func (c *Client) statusError(u *url.URL, status int, auth bool) error {
	switch {
	case auth && (status == 401 || status == 403 || status == 404):
		msg := fmt.Sprintf("GitHub answered %d to the token from %s: it cannot read %s, or that release does not exist", status, c.Token.Source, Repo)
		if c.Token.Source != "gh auth token" {
			// An exported variable wins over `gh auth`, so a stale one is the usual cause.
			msg += ". A stale GH_TOKEN or GITHUB_TOKEN wins over `gh auth`: unset it, or set OUTFIT_GITHUB_TOKEN to the one you want"
		}
		return fixf("gh auth login   (or set OUTFIT_GITHUB_TOKEN to a token that can read "+Repo+")", "%s", msg)
	case !auth && (status == 401 || status == 403 || status == 404):
		return fixf("gh auth login   (or set OUTFIT_GITHUB_TOKEN)",
			"could not download %s (HTTP %d): that release does not exist, or the repository is private and needs a GitHub token", u.Path, status)
	}
	return fixf("", "GitHub answered %d for %s", status, u.Path)
}

// ---- release layout -------------------------------------------------------

type apiRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"assets"`
}

// source serves the files of one release, through the API (token) or the public URLs.
type source struct {
	c      *Client
	tag    string // "v0.3.1", known only on the token path
	assets map[string]int64
	pub    string // public base for this release, token-less path
}

func (c *Client) open(ctx context.Context, want *Version) (*source, error) {
	if c.Token.Value != "" {
		rel := "latest"
		if want != nil {
			rel = "tags/v" + want.String()
		}
		raw, err := c.get(ctx, c.api()+"/releases/"+rel, "application/vnd.github+json", true, c.metaTimeout(), maxMeta)
		if err != nil {
			return nil, err
		}
		var r apiRelease
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, fixf("", "GitHub's release answer is not readable: %v", err)
		}
		if _, err := ParseVersion(r.TagName); err != nil {
			return nil, fixf("", "the release has no usable tag name (%q)", r.TagName)
		}
		s := &source{c: c, tag: r.TagName, assets: map[string]int64{}}
		for _, a := range r.Assets {
			s.assets[a.Name] = a.ID
		}
		if len(s.assets) == 0 {
			return nil, fixf("", "release %s lists no downloadable files (is it still a draft?)", r.TagName)
		}
		return s, nil
	}
	pub := c.base() + "/releases/latest/download"
	if want != nil {
		pub = c.base() + "/releases/download/v" + want.String()
	}
	return &source{c: c, pub: pub}, nil
}

// file downloads one asset of the release.
func (s *source) file(ctx context.Context, name string, timeout time.Duration, limit int64) ([]byte, error) {
	if s.c.Token.Value != "" {
		id, ok := s.assets[name]
		if !ok {
			return nil, fixf("", "release %s has no file named %s", s.tag, name)
		}
		return s.c.get(ctx, fmt.Sprintf("%s/releases/assets/%d", s.c.api(), id), "application/octet-stream", true, timeout, limit)
	}
	return s.c.get(ctx, s.pub+"/"+name, "", false, timeout, limit)
}

var sumLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S+)$`)

// archiveFor picks the one archive for plat out of checksums.txt, and its hash.
func archiveFor(checksums []byte, plat string) (name, sum string, v Version, err error) {
	re := regexp.MustCompile(`^outfit_([0-9]+\.[0-9]+\.[0-9]+)_` + regexp.QuoteMeta(plat) + `\.tar\.gz$`)
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(checksums), "\n"), "\n") {
		m := sumLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		if vm := re.FindStringSubmatch(m[2]); vm != nil {
			n++
			name, sum = m[2], m[1]
			v, _ = ParseVersion(vm[1])
		}
	}
	if n != 1 {
		return "", "", Version{}, fixf("", "checksums.txt does not list exactly one %s build", plat)
	}
	return name, sum, v, nil
}

// Latest reports the newest release's version without downloading it. It is
// information for `--check` and the banner: installing re-derives and verifies
// everything.
func (c *Client) Latest(ctx context.Context) (Version, error) {
	plat, err := Platform(c.goos(), c.goarch())
	if err != nil {
		return Version{}, err
	}
	s, err := c.open(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	if s.tag != "" {
		return ParseVersion(s.tag)
	}
	sums, err := s.file(ctx, "checksums.txt", c.metaTimeout(), maxMeta)
	if err != nil {
		return Version{}, err
	}
	_, _, v, err := archiveFor(sums, plat)
	return v, err
}

// Fetch downloads the release (the latest, or exactly want), verifies the
// signature of its checksum list against the compiled-in keys, then the archive's
// SHA-256 against it, and unpacks the executable. Fail closed at every step.
func (c *Client) Fetch(ctx context.Context, want *Version) (*Package, error) {
	if len(c.Keys) == 0 {
		return nil, fixf("reinstall with install.sh (docs/install.md)",
			"this build has no trusted release keys, so it cannot verify an update (is it a development build?)")
	}
	plat, err := Platform(c.goos(), c.goarch())
	if err != nil {
		return nil, err
	}
	s, err := c.open(ctx, want)
	if err != nil {
		return nil, err
	}
	sums, err := s.file(ctx, "checksums.txt", c.metaTimeout(), maxMeta)
	if err != nil {
		return nil, err
	}
	sig, err := s.file(ctx, "checksums.txt.minisig", c.metaTimeout(), maxMeta)
	if err != nil {
		return nil, err
	}
	if err := Verify(c.Keys, sums, sig); err != nil {
		return nil, fixf("", "%v; nothing was installed", err)
	}
	name, sum, v, err := archiveFor(sums, plat)
	if err != nil {
		return nil, err
	}
	if want != nil && v != *want {
		return nil, fixf("", "asked for %s but the release holds %s", want, name)
	}
	if s.tag != "" {
		if tv, _ := ParseVersion(s.tag); tv != v {
			return nil, fixf("", "release %s holds %s, which is not its own build; nothing was installed", s.tag, name)
		}
	}
	archive, err := s.file(ctx, name, c.downloadTimeout(), maxArchive)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != sum {
		return nil, fixf("", "SHA-256 mismatch for %s (expected %s, got %s); nothing was installed", name, sum, hex.EncodeToString(got[:]))
	}
	bin, err := ExtractBinary(archive)
	if err != nil {
		return nil, fixf("", "%s: %v; nothing was installed", name, err)
	}
	return &Package{Version: v, Archive: name, Binary: bin}, nil
}

func (c *Client) goos() string {
	if c.GOOS != "" {
		return c.GOOS
	}
	return runtimeGOOS
}

func (c *Client) goarch() string {
	if c.GOARCH != "" {
		return c.GOARCH
	}
	return runtimeGOARCH
}
