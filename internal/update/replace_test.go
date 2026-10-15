package update

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestReplaceIsAtomicAndKeepsTheMode(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "outfit")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Replace(exe, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "new" {
		t.Fatalf("content = %q", b)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	want, _ := filepath.EvalSymlinks(exe)
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	// No temp file left behind.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftovers: %v", entries)
	}
}

func TestReplaceFollowsSymlinks(t *testing.T) {
	real := t.TempDir()
	link := t.TempDir()
	target := filepath.Join(real, "outfit-1.2.3")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(link, "outfit")); err != nil {
		t.Fatal(err)
	}
	if _, err := Replace(filepath.Join(link, "outfit"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(filepath.Join(link, "outfit")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced by a file: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "new" {
		t.Fatalf("target = %q", b)
	}
}

func TestReplaceReadOnlyDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "outfit")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	_, err := Replace(exe, []byte("new"))
	var nw *NotWritableError
	if !errors.As(err, &nw) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatalf("the binary changed: %q", b)
	}
}

func TestReplaceSweepsStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "outfit")
	_ = os.WriteFile(exe, []byte("old"), 0o755)
	stale := filepath.Join(dir, ".outfit-update-stale")
	fresh := filepath.Join(dir, ".outfit-update-fresh")
	for _, p := range []string{stale, fresh} {
		_ = os.WriteFile(p, []byte("x"), 0o644)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(stale, old, old)
	if _, err := Replace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("an hour-old leftover temp file was not removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a fresh temp file (maybe a running update's) was removed")
	}
}

func TestReplaceMissingExecutable(t *testing.T) {
	if _, err := Replace(filepath.Join(t.TempDir(), "nope"), []byte("x")); err == nil {
		t.Fatal("replaced something that is not there")
	}
}

func TestLockSurvivesALockFileOwnedBySomeoneElse(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "update.lock"), nil, 0o444); err != nil { // read-only, like root's after a sudo run
		t.Fatal(err)
	}
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatalf("a read-only lock file stopped the lock: %v", err)
	}
	unlock()
}

func TestLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(dir); !errors.Is(err, ErrLocked) {
		t.Fatalf("second lock: %v", err)
	}
	unlock()
	unlock2, err := Lock(dir)
	if err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	unlock2()
}

func TestCacheDir(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if d := CacheDir(get(map[string]string{})); d != "" {
		t.Fatalf("no HOME: %q", d)
	}
	if d := CacheDir(get(map[string]string{"HOME": "/h"})); d == "" || filepath.Base(d) != "outfit" {
		t.Fatalf("HOME: %q", d)
	}
}

// ---- banner ----------------------------------------------------------------

func banner(t *testing.T, current string, check func(context.Context) (Version, error)) *Banner {
	return &Banner{Current: current, Dir: t.TempDir(), Check: check, Budget: 200 * time.Millisecond}
}

func newer(v string) func(context.Context) (Version, error) {
	return func(context.Context) (Version, error) { return *mustV(v), nil }
}

func mustV(s string) *Version {
	v, err := ParseVersion(s)
	if err != nil {
		panic(err)
	}
	return &v
}

func TestBannerChecksOncePerDayAndCaches(t *testing.T) {
	var calls atomic.Int32
	check := func(context.Context) (Version, error) { calls.Add(1); return *mustV("0.2.0"), nil }
	b := banner(t, "0.1.0", check)
	b.Start(context.Background())
	if got := b.Finish(); got != "Update available: 0.1.0 → 0.2.0. Run outfit update" {
		t.Fatalf("banner = %q", got)
	}
	// A second command within the day: from the cache, no network.
	b2 := &Banner{Current: "0.1.0", Dir: b.Dir, Check: check, Budget: 200 * time.Millisecond}
	b2.Start(context.Background())
	if got := b2.Finish(); got == "" {
		t.Fatal("the cached banner did not show")
	}
	if calls.Load() != 1 {
		t.Fatalf("network checks = %d, want 1", calls.Load())
	}
	// A day later: checks again.
	b3 := &Banner{Current: "0.1.0", Dir: b.Dir, Check: check, Budget: 200 * time.Millisecond, Now: func() time.Time { return time.Now().Add(25 * time.Hour) }}
	b3.Start(context.Background())
	b3.Finish()
	if calls.Load() != 2 {
		t.Fatalf("network checks = %d, want 2", calls.Load())
	}
}

func TestBannerSilentWhenUpToDateOrDevOrNoDir(t *testing.T) {
	for name, b := range map[string]*Banner{
		"up to date": banner(t, "0.2.0", newer("0.2.0")),
		"ahead":      banner(t, "0.3.0", newer("0.2.0")),
		"dev build":  banner(t, "dev", newer("0.2.0")),
		"no cache":   {Current: "0.1.0", Check: newer("0.2.0")},
	} {
		b.Start(context.Background())
		if got := b.Finish(); got != "" {
			t.Errorf("%s: banner %q", name, got)
		}
	}
}

func TestBannerSlowCheckIsSkippedAndNotCached(t *testing.T) {
	slow := func(ctx context.Context) (Version, error) {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
		}
		return *mustV("0.2.0"), nil
	}
	b := banner(t, "0.1.0", slow)
	start := time.Now()
	b.Start(context.Background())
	if got := b.Finish(); got != "" {
		t.Fatalf("banner %q from a check that was not done", got)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Finish waited %v for a slow check", time.Since(start))
	}
	if _, ok := readCache(b.Dir); ok {
		t.Fatal("the cache was written without a successful check")
	}
}

func TestBannerFailedCheckIsRememberedOnlyAsABackOff(t *testing.T) {
	b := banner(t, "0.1.0", func(context.Context) (Version, error) { return Version{}, errors.New("offline") })
	b.Start(context.Background())
	if got := b.Finish(); got != "" {
		t.Fatalf("banner %q", got)
	}
	c, ok := readCache(b.Dir)
	if !ok || !c.Failed || c.Latest != "" {
		t.Fatalf("cache = %+v, %v: a failure must be a back-off marker, never an answer", c, ok)
	}
}

func TestBannerBacksOffAfterAFailedCheckButNotAfterATimeout(t *testing.T) {
	var calls atomic.Int32
	failing := func(context.Context) (Version, error) { calls.Add(1); return Version{}, errors.New("HTTP 404") }
	b := banner(t, "0.1.0", failing)
	b.Start(context.Background())
	b.Finish()
	// Within the hour: no second attempt, nothing printed.
	b2 := &Banner{Current: "0.1.0", Dir: b.Dir, Check: failing, Budget: 200 * time.Millisecond}
	b2.Start(context.Background())
	if got := b2.Finish(); got != "" {
		t.Fatalf("banner %q after a failure", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("checks = %d: a failed check was retried within the back-off", calls.Load())
	}
	// After the back-off it tries again.
	b3 := &Banner{Current: "0.1.0", Dir: b.Dir, Check: failing, Budget: 200 * time.Millisecond, Now: func() time.Time { return time.Now().Add(2 * time.Hour) }}
	b3.Start(context.Background())
	b3.Finish()
	if calls.Load() != 2 {
		t.Fatalf("checks = %d, want 2 after the back-off", calls.Load())
	}

	// A check cut off by the budget says nothing about the network: not remembered.
	cut := func(ctx context.Context) (Version, error) { <-ctx.Done(); return Version{}, ctx.Err() }
	b4 := banner(t, "0.1.0", cut)
	b4.Start(context.Background())
	b4.Finish()
	if _, ok := readCache(b4.Dir); ok {
		t.Fatal("a timed-out check was cached as a failure")
	}
}

func TestCacheDirIgnoresARelativeXDGCacheHome(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if d := CacheDir(get(map[string]string{"XDG_CACHE_HOME": ".cache", "HOME": "/h"})); !filepath.IsAbs(d) {
		t.Fatalf("a relative XDG_CACHE_HOME was used: %q", d)
	}
}

// The real check cut off by the budget must not be cached as a failure: its error
// has to say "deadline", through the *Error the client returns.
func TestBannerTimeoutFromTheRealClientIsNotCached(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	c := &Client{BaseURL: slow.URL, GOOS: "linux", GOARCH: "amd64", MetaTimeout: time.Minute}
	b := &Banner{Current: "0.1.0", Dir: t.TempDir(), Check: c.Latest, Budget: 100 * time.Millisecond}
	b.Start(context.Background())
	time.Sleep(400 * time.Millisecond) // the command outlives the budget; the check hit its deadline
	if got := b.Finish(); got != "" {
		t.Fatalf("banner %q", got)
	}
	if cf, ok := readCache(b.Dir); ok {
		t.Fatalf("a deadline was cached as %+v: it says nothing about the network", cf)
	}
}

func TestIsTimeout(t *testing.T) {
	if !IsTimeout(context.DeadlineExceeded) || !IsTimeout(&Error{Cause: context.Canceled}) || IsTimeout(errors.New("HTTP 404")) {
		t.Fatal("IsTimeout misclassifies")
	}
}

func TestCacheFilesArePrivateAndNeedAnAbsoluteDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "outfit")
	unlock, err := Lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := writeCache(dir, cacheFile{CheckedAt: time.Now(), Latest: "0.2.0"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{dir: 0o700, filepath.Join(dir, "update.lock"): 0o600, filepath.Join(dir, "update.json"): 0o600} {
		fi, err := os.Stat(path)
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, %v; want %v", path, fi.Mode().Perm(), err, want)
		}
	}
	// A relative directory is refused, never created next to the cwd.
	if _, err := Lock("relative/cache"); err == nil {
		t.Error("a relative lock directory was accepted")
	}
	if err := writeCache("relative/cache", cacheFile{CheckedAt: time.Now()}); err == nil {
		t.Error("a relative cache directory was accepted")
	}
	if _, ok := readCache("relative/cache"); ok {
		t.Error("a relative cache directory was read")
	}
	b := &Banner{Current: "0.1.0", Dir: "relative/cache", Check: newer("0.2.0")}
	b.Start(context.Background())
	if got := b.Finish(); got != "" {
		t.Errorf("banner %q with a relative cache directory", got)
	}
}
