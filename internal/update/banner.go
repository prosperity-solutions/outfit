package update

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// BannerCommands are the human-facing commands that may print the update banner
// (HANDOVER.md §5). Never sync, get, hook or diff: those run inside git hooks and
// must stay fast.
var BannerCommands = map[string]bool{"status": true, "ls": true, "use": true, "set": true, "init": true, "show": true, "new": true}

// CacheDir is where outfit keeps update.json and update.lock: the user cache
// directory, derived from the given environment so tests can point it anywhere.
func CacheDir(getenv func(string) string) string {
	// Like os.UserCacheDir, an XDG_CACHE_HOME that is not absolute is ignored: a
	// relative one would put the cache inside whatever repository outfit runs in.
	if d := getenv("XDG_CACHE_HOME"); filepath.IsAbs(d) && runtime.GOOS != "darwin" {
		return filepath.Join(d, "outfit")
	}
	home := getenv("HOME")
	if home == "" {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Caches", "outfit")
	}
	return filepath.Join(home, ".cache", "outfit")
}

type cacheFile struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest,omitempty"`
	// Failed marks a check that got a definite error (no token, offline, 404): it
	// is not retried for FailureBackoff, so a user who can never reach the release
	// does not pay a network round trip and a `gh` process on every command.
	Failed bool `json:"failed,omitempty"`
}

func readCache(dir string) (cacheFile, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "update.json"))
	if err != nil {
		return cacheFile{}, false
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil || c.CheckedAt.IsZero() {
		return cacheFile{}, false
	}
	return c, true
}

// writeCache is atomic, so a reader never sees half a file. Errors are ignored by
// the caller: a banner is never worth failing a command.
func writeCache(dir string, c cacheFile) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(c)
	f, err := os.CreateTemp(dir, ".update-*")
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "update.json"))
}

// Banner decides whether to tell the user a newer release exists. It checks at most
// every 24 hours, caches the answer, and never makes a command wait more than
// Budget for the network.
type Banner struct {
	Current  string // this build's version
	Dir      string // cache dir; "" disables the banner
	Check    func(ctx context.Context) (Version, error)
	Now      func() time.Time
	Budget   time.Duration // default 800 ms
	Interval time.Duration // default 24 h
	// FailureBackoff is how long a failed check is not retried. Default 1 h.
	FailureBackoff time.Duration

	cur     Version
	started time.Time
	result  chan checkResult
	cached  *Version
	skip    bool
}

type checkResult struct {
	v   Version
	err error
}

// Start reads the cache and, when it is stale or missing, begins the check in a
// goroutine. Call it at the start of an eligible command, and Finish at the end.
func (b *Banner) Start(ctx context.Context) {
	cur, err := ParseVersion(b.Current)
	if err != nil || b.Dir == "" || b.Check == nil {
		return // a development build, or no cache dir: nothing to compare
	}
	b.cur = cur
	if b.Now == nil {
		b.Now = time.Now
	}
	if b.Budget == 0 {
		b.Budget = 800 * time.Millisecond
	}
	if b.Interval == 0 {
		b.Interval = 24 * time.Hour
	}
	if b.FailureBackoff == 0 {
		b.FailureBackoff = time.Hour
	}
	b.started = b.Now()
	if c, ok := readCache(b.Dir); ok {
		age := b.started.Sub(c.CheckedAt)
		if c.Failed && age < b.FailureBackoff {
			b.skip = true // failed recently: say nothing, ask nothing
			return
		}
		if v, err := ParseVersion(c.Latest); err == nil && !c.Failed && age < b.Interval {
			b.cached = &v
			return
		}
	}
	b.result = make(chan checkResult, 1)
	cctx, cancel := context.WithTimeout(ctx, b.Budget)
	go func() {
		defer cancel()
		v, err := b.Check(cctx)
		b.result <- checkResult{v, err}
	}()
}

// Finish returns the banner line, or "" when there is nothing to say. It waits for
// the check only as long as the budget leaves; a check that is not done is skipped
// and tried again next time. The cache is written only after a successful check.
func (b *Banner) Finish() string {
	if b.skip {
		return ""
	}
	var latest *Version
	switch {
	case b.cached != nil:
		latest = b.cached
	case b.result != nil:
		wait := b.Budget - b.Now().Sub(b.started)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		defer timer.Stop()
		var r checkResult
		select {
		case r = <-b.result: // already finished: take it even if the budget is spent
		default:
			select {
			case r = <-b.result:
			case <-timer.C:
				return ""
			}
		}
		if r.err != nil {
			// A check cut off by the budget is not a verdict: try again next time.
			// Anything else (offline, 404, no token) is remembered for a while.
			if !errors.Is(r.err, context.DeadlineExceeded) && !errors.Is(r.err, context.Canceled) {
				_ = writeCache(b.Dir, cacheFile{CheckedAt: b.Now(), Failed: true})
			}
			return ""
		}
		_ = writeCache(b.Dir, cacheFile{CheckedAt: b.Now(), Latest: r.v.String()})
		latest = &r.v
	default:
		return ""
	}
	if b.cur.Less(*latest) {
		return "Update available: " + b.cur.String() + " → " + latest.String() + ". Run outfit update"
	}
	return ""
}
