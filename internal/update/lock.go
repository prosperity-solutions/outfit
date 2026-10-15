package update

import (
	"errors"
	"os"
	"syscall"
)

// ErrLocked means another `outfit update` holds the lock.
var ErrLocked = errors.New("another outfit update is running")

// Lock takes the update lock in dir (a flock on update.lock: the kernel drops it
// if the process dies, so there is no stale lock to clean). The caller runs the
// returned function when done.
func Lock(dir string) (func(), error) {
	root, err := openCacheRoot(dir, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	// Read-only is enough for flock, and works on a lock file left behind by a
	// `sudo outfit update` that now belongs to root.
	f, err := root.OpenFile("update.lock", os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
