package apply

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Lock is the per-worktree sync lock: `<git-dir>/outfit/sync.lock`, created with
// O_EXCL and holding the owner's pid.
type Lock struct{ path string }

const (
	lockWait  = 15 * time.Second // how long a second sync waits for the first
	lockStale = 10 * time.Minute // a lock older than this is dead whatever its pid says
)

// AcquireLock takes the lock, waiting for a running sync to finish: syncs are
// serialised, never skipped, so the last one to run sees everything the others
// did. A lock whose owner is gone (kill -9, power loss) is stale: it is removed and
// taken over.
func AcquireLock(gitDir string) (*Lock, error) {
	path := filepath.Join(gitDir, "outfit", "sync.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, errors.Join(werr, cerr)
			}
			return &Lock{path: path}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if stale(path) {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another outfit sync has held %s for over %s", path, lockWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Release drops the lock.
func (l *Lock) Release() { _ = os.Remove(l.path) }

// stale reports whether the lock file's owner is gone or it is very old.
func stale(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false // gone already: the next O_EXCL decides
	}
	if time.Since(fi.ModTime()) > lockStale {
		return true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		// Half-written (the owner is between create and write): give it a moment,
		// unless it has been that way for a while.
		return time.Since(fi.ModTime()) > 2*time.Second
	}
	if err := syscall.Kill(pid, 0); err != nil && errors.Is(err, syscall.ESRCH) {
		return true
	}
	return false
}
