package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"aead.dev/minisign"
)

// Error is a failure with the exact fix to run, which is what agents and people
// follow. The CLI turns it into its own error type.
type Error struct {
	Msg       string
	Fix       string
	Retryable bool  // a transient failure (network, 5xx, 429): the same command may work later
	Cause     error // what went wrong underneath, for errors.Is/As (a deadline, say)
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Cause }

// IsTimeout reports whether err was caused by a deadline or cancellation: a check
// cut short by its time budget, which says nothing about the network.
func IsTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		(errors.As(err, &ne) && ne.Timeout())
}

func fixf(fix, format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Fix: fix}
}

// retryf is a transient failure: trying the same command again is the fix. cause (may be nil) is kept
// for errors.Is/As; its text, with the token scrubbed, is part of the message.
func retryf(cause error, format string, a ...any) *Error {
	// No Fix: the command to retry depends on what was asked (--check, --version),
	// so the CLI fills it in.
	return &Error{Msg: fmt.Sprintf(format, a...), Retryable: true, Cause: cause}
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
	// MetaTimeout bounds each small request (release metadata, checksums, signature)
	// as a whole: 5 s. The archive is bounded by IdleTimeout (no data for that long:
	// 15 s) and by DownloadTimeout in total (10 min), so a slow link that keeps
	// delivering is not cut off at an arbitrary second.
	MetaTimeout     time.Duration
	IdleTimeout     time.Duration
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

func isLoopback(u *url.URL) bool {
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

// secureEnough reports whether a request may follow a redirect to u: https, or http
// to this machine.
func secureEnough(u *url.URL) bool {
	return u.Scheme == "https" || (u.Scheme == "http" && isLoopback(u))
}

// tokenHostOK reports whether the GitHub token may be sent to u: only the real API
// host over https, or this machine (tests). OUTFIT_API_URL can name another place,
// and a token must never follow it there: the signature still protects the binary,
// but the token has no such protection.
func tokenHostOK(u *url.URL) bool {
	return (u.Scheme == "https" && u.Hostname() == "api.github.com" && u.User == nil) ||
		((u.Scheme == "http" || u.Scheme == "https") && isLoopback(u) && u.User == nil)
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
	return 10 * time.Minute
}

func (c *Client) idleTimeout() time.Duration {
	if c.IdleTimeout > 0 {
		return c.IdleTimeout
	}
	return 15 * time.Second
}

// resetReader restarts a timer whenever data arrives: an idle timeout.
type resetReader struct {
	r    io.Reader
	t    *time.Timer
	idle time.Duration
}

func (r *resetReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		r.t.Reset(r.idle)
	}
	return n, err
}

// get fetches one URL, bounded in time and size. auth sends the token, and
// refuses to when the URL is not secure.
func (c *Client) get(ctx context.Context, rawURL, accept string, auth bool, timeout, idle time.Duration, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	if auth && !tokenHostOK(u) {
		return nil, fixf("unset OUTFIT_API_URL", "refusing to send a GitHub token to %s (it goes only to api.github.com over https; OUTFIT_API_URL is for tests and may only name this machine)", u.Host)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var idleTimer *time.Timer
	if idle > 0 {
		idleTimer = time.AfterFunc(idle, cancel)
		defer idleTimer.Stop()
	}
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
		return nil, retryf(err, "could not reach %s: %v", u.Host, scrub(err, c.Token.Value))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, c.statusError(u, resp.StatusCode, auth)
	}
	var body io.Reader = resp.Body
	if idleTimer != nil {
		idleTimer.Reset(idle)
		body = &resetReader{r: resp.Body, t: idleTimer, idle: idle}
	}
	b, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, retryf(err, "could not read from %s: %v", u.Host, scrub(err, c.Token.Value))
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
		if c.Token.Source == "gh auth token" {
			// The active gh account cannot read the repository: another may.
			return fixf("gh auth status   (shows the active account; then `gh auth switch`, or `gh auth login` with one that can read "+Repo+")", "%s", msg)
		}
		// An exported variable wins over `gh auth`, so logging in again would not help.
		return fixf("unset "+c.Token.Source+"   (or set OUTFIT_GITHUB_TOKEN to a token that can read "+Repo+")",
			"%s. An exported token wins over `gh auth`", msg)
	case !auth && (status == 401 || status == 403 || status == 404):
		fix := "gh auth login   (or set OUTFIT_GITHUB_TOKEN)"
		if _, err := exec.LookPath("gh"); err != nil {
			fix = "install the GitHub CLI (brew install gh, or https://cli.github.com), then gh auth login   (or set OUTFIT_GITHUB_TOKEN)"
		}
		return fixf(fix, "could not download %s%s (HTTP %d): that release does not exist, or the repository is private and needs a GitHub token", u.Host, u.Path, status)
	}
	if status >= 500 || status == 429 {
		return retryf(nil, "GitHub answered %d for %s", status, u.Path)
	}
	return fixf("outfit update --check", "GitHub answered %d for %s", status, u.Path)
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
		raw, err := c.get(ctx, c.api()+"/releases/"+rel, "application/vnd.github+json", true, c.metaTimeout(), 0, maxMeta)
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
func (s *source) file(ctx context.Context, name string, timeout, idle time.Duration, limit int64) ([]byte, error) {
	if s.c.Token.Value != "" {
		id, ok := s.assets[name]
		if !ok {
			return nil, fixf("", "release %s has no file named %s", s.tag, name)
		}
		return s.c.get(ctx, fmt.Sprintf("%s/releases/assets/%d", s.c.api(), id), "application/octet-stream", true, timeout, idle, limit)
	}
	return s.c.get(ctx, s.pub+"/"+name, "", false, timeout, idle, limit)
}

var sumLine = regexp.MustCompile(`^([0-9a-f]{64})  (\S+)$`)

// archiveFor picks the one archive for plat out of checksums.txt, and its hash.
func archiveFor(checksums []byte, plat string) (name, sum string, v Version, err error) {
	num := `(?:0|[1-9][0-9]*)`
	re := regexp.MustCompile(`^outfit_(` + num + `\.` + num + `\.` + num + `)_` + regexp.QuoteMeta(plat) + `\.tar\.gz$`)
	n := 0
	for _, line := range strings.Split(strings.TrimRight(string(checksums), "\n"), "\n") {
		m := sumLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		if vm := re.FindStringSubmatch(m[2]); vm != nil {
			n++
			name, sum = m[2], m[1]
			pv, perr := ParseVersion(vm[1])
			if perr != nil {
				return "", "", Version{}, fixf("", "checksums.txt lists %s, which is not a version outfit understands", m[2])
			}
			v = pv
		}
	}
	if n != 1 {
		return "", "", Version{}, fixf("", "checksums.txt does not list exactly one %s build", plat)
	}
	return name, sum, v, nil
}

// Latest reports the newest release's version without downloading anything beyond
// the release listing. It is UNAUTHENTICATED information, good enough for the update
// banner: anything that acts on the answer uses LatestVerified or Fetch.
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
	sums, err := s.file(ctx, "checksums.txt", c.metaTimeout(), 0, maxMeta)
	if err != nil {
		return Version{}, err
	}
	_, _, v, err := archiveFor(sums, plat)
	return v, err
}

// release is one release whose checksum list has been verified.
type release struct {
	s    *source
	name string  // the archive for this platform
	sum  string  // its SHA-256, from the signed list
	v    Version // its version
}

// verified opens the release (the latest, or exactly want) and checks everything
// that can be checked without the archive: the minisign signature of checksums.txt
// against the compiled-in keys, that it lists exactly one build for this platform,
// that it is the version asked for, and (token path) that it is the release's own
// build. Fail closed at every step.
func (c *Client) verified(ctx context.Context, want *Version) (*release, error) {
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
	sums, err := s.file(ctx, "checksums.txt", c.metaTimeout(), 0, maxMeta)
	if err != nil {
		return nil, err
	}
	sig, err := s.file(ctx, "checksums.txt.minisig", c.metaTimeout(), 0, maxMeta)
	if err != nil {
		return nil, err
	}
	if err := Verify(c.Keys, sums, sig); err != nil {
		return nil, fixf("outfit update   (try again; if it keeps failing, reinstall: docs/install.md)", "%v; nothing was installed", err)
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
	if s.tag == "" {
		// Token-less path: the checksum list came from "latest"; fetch the archive from
		// the exact version it names, so a release published in between cannot make the
		// archive 404 under the old name.
		s.pub = c.base() + "/releases/download/v" + v.String()
	}
	return &release{s: s, name: name, sum: sum, v: v}, nil
}

// LatestVerified is the newest release's version, read from a checksum list whose
// signature checked out: what `outfit update` and `update --check` decide on, so
// an unsigned answer cannot make outfit believe it is up to date.
func (c *Client) LatestVerified(ctx context.Context) (Version, error) {
	r, err := c.verified(ctx, nil)
	if err != nil {
		return Version{}, err
	}
	return r.v, nil
}

// Fetch downloads the release (the latest, or exactly want) after verified(), checks
// the archive's SHA-256 against the signed list, and unpacks the executable.
func (c *Client) Fetch(ctx context.Context, want *Version) (*Package, error) {
	r, err := c.verified(ctx, want)
	if err != nil {
		return nil, err
	}
	archive, err := r.s.file(ctx, r.name, c.downloadTimeout(), c.idleTimeout(), maxArchive)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != r.sum {
		return nil, fixf("outfit update   (try again; if it keeps failing, reinstall: docs/install.md)",
			"SHA-256 mismatch for %s (expected %s, got %s); nothing was installed", r.name, r.sum, hex.EncodeToString(got[:]))
	}
	bin, err := ExtractBinary(archive)
	if err != nil {
		return nil, fixf("", "%s: %v; nothing was installed", r.name, err)
	}
	return &Package{Version: r.v, Archive: r.name, Binary: bin}, nil
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
