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

// ActiveEnv names the hook outfit is currently dispatching, as "<hook>@<common
// dir>". If the same hook of the SAME repository asks to be dispatched again,
// something chains into outfit's own shims (a team hook symlinked to a shim, a
// hooksPath that loops): refuse instead of forking forever. The repository is
// part of the key because the variable is inherited by everything the team hook
// starts: a hook that commits in ANOTHER outfit repository must not have that
// repository's hooks (and guard) skipped.
const ActiveEnv = "OUTFIT_HOOK_ACTIVE"

// ActiveKey builds the ActiveEnv value.
func ActiveKey(name, commonDir string) string { return name + "@" + filepath.Clean(commonDir) }

// Chain runs the team's original hook `name` from origDir, passing args, stdin
// and the environment through, and returns its exit code EXACTLY. A missing or
// non-executable hook is not an error: git skips those too. The working
// directory is the caller's: git decides it per hook (the worktree root for
// client hooks, $GIT_DIR for receive-side ones).
func Chain(ctx context.Context, origDir, name, activeKey string, args []string, stdin io.Reader, stdout, stderr io.Writer) (code int, ran bool) {
	path := filepath.Join(origDir, name)
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode()&0o111 == 0 {
		return 0, false
	}
	if IsOurs(path) { // a team "hook" that is really one of our shims
		_, _ = io.WriteString(stderr, "outfit: "+path+" is an outfit shim, not a team hook: not chaining into it\n")
		return 0, false
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Env = append(os.Environ(), ActiveEnv+"="+activeKey)
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
