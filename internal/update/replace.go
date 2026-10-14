package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

// NotWritableError is returned when the directory holding the executable cannot
// be written to. Nothing was changed.
type NotWritableError struct{ Dir string }

func (e *NotWritableError) Error() string {
	return fmt.Sprintf("%s is not writable, so outfit cannot replace itself there", e.Dir)
}

// Replace puts bin at the running executable's real path, atomically: a temp file
// in the SAME directory (so the rename cannot cross a filesystem), mode 0755, then
// os.Rename. A running process keeps its old inode, so the update is safe while
// outfit runs, and a hook shim that points at the path keeps working. Symlinks are
// followed, so `/usr/local/bin/outfit -> …/outfit` updates the file, not the link.
// It never signs anything: the release's signature travels inside bin.
func Replace(exe string, bin []byte) (string, error) {
	real, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("cannot resolve %s: %w", exe, err)
	}
	dir := filepath.Dir(real)
	f, err := os.CreateTemp(dir, ".outfit-update-*")
	if err != nil {
		if errors.Is(err, fs.ErrPermission) || errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) {
			return "", &NotWritableError{Dir: dir}
		}
		return "", err
	}
	tmp := f.Name()
	cleanup := func() { _ = os.Remove(tmp) }
	if _, err := f.Write(bin); err != nil {
		_ = f.Close()
		cleanup()
		return "", err
	}
	if err := f.Chmod(0o755); err != nil {
		_ = f.Close()
		cleanup()
		return "", err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		cleanup()
		return "", err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", err
	}
	if err := os.Rename(tmp, real); err != nil {
		cleanup()
		return "", err
	}
	if runtime.GOOS == "darwin" {
		// Best effort, and only ever removes the quarantine flag: a download by
		// curl never sets it, but a copy made by a browser would. Never re-sign.
		_ = exec.Command("xattr", "-d", "com.apple.quarantine", real).Run()
	}
	return real, nil
}
