package update

import (
	"context"
	"encoding/json"
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
	if d := getenv("XDG_CACHE_HOME"); d != "" && runtime.GOOS != "darwin" {
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
	Latest    string    `json:"latest"`
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

	cur     Version
	started time.Time
	result  chan checkResult
	cached  *Version
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
	b.started = b.Now()
	if c, ok := readCache(b.Dir); ok && b.started.Sub(c.CheckedAt) < b.Interval {
		if v, err := ParseVersion(c.Latest); err == nil {
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
