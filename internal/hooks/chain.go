package hooks

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// Chain runs the team's original hook `name` from origDir, passing args, stdin
// and the environment through, and returns its exit code EXACTLY. A missing or
// non-executable hook is not an error: git skips those too.
func Chain(ctx context.Context, origDir, name string, args []string, stdin io.Reader, stdout, stderr io.Writer, cwd string) (code int, ran bool) {
	path := filepath.Join(origDir, name)
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode()&0o111 == 0 {
		return 0, false
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = cwd
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Env = os.Environ()
	err = cmd.Run()
	if err == nil {
		return 0, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), true
		}
		return ee.ExitCode(), true
	}
	// The hook could not be started (bad interpreter, ...): what git reports.
	return 126, true
}
