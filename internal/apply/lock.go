package apply

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Lock is the per-worktree sync lock: an flock(2) on `<git-dir>/outfit/sync.lock`.
// The kernel drops it when the process dies, however it dies, so there is no stale
// lock to detect and no pid to trust. The file itself is never removed: removing it
// would let two syncs hold "the lock" on two different files.
type Lock struct{ f *os.File }

const lockWait = 15 * time.Second // how long a second sync waits for the first

// AcquireLock takes the lock, waiting for a running sync to finish: syncs are
// serialised, never skipped, so the last one to run sees everything the others did.
func AcquireLock(gitDir string) (*Lock, error) {
	path := filepath.Join(gitDir, "outfit", "sync.lock")
	// Rooted at the git directory: the lock can only ever be made inside it.
	root, err := os.OpenRoot(gitDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	if err := root.MkdirAll("outfit", 0o750); err != nil {
		return nil, err
	}
	f, err := root.OpenFile("outfit/sync.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EINTR {
			_ = f.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("another outfit sync has held %s for over %s", path, lockWait)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Release drops the lock.
func (l *Lock) Release() { _ = l.f.Close() }
