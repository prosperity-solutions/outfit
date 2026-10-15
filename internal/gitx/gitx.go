// Package gitx is the ONLY place outfit execs git. It counts calls so tests can
// hold the hot paths to their budgets (sync <= 2, get <= 1), and it sets
// GIT_OPTIONAL_LOCKS=0 on reads so a read never races a writer for index.lock.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// TraceEnv names a file; when set, every git call appends one line to it. It
// is how end-to-end tests count the git processes of a real outfit binary.
const TraceEnv = "OUTFIT_GIT_TRACE_FILE"

// calls counts git processes spawned by this process.
var calls atomic.Int64

// Calls returns the number of git processes spawned by this process so far.
func Calls() int64 { return calls.Load() }

// ResetCalls zeroes the counter. Tests use it around the code under budget.
func ResetCalls() { calls.Store(0) }

// Client runs git in Dir. The zero value runs in the process's cwd.
type Client struct {
	// Dir is the working directory for every call ("" = inherit).
	Dir string
	// Env is appended to the process environment for every call.
	Env []string
	// OwnRepo drops the variables that point git at a particular repository (GIT_DIR,
	// GIT_WORK_TREE, GIT_INDEX_FILE, GIT_COMMON_DIR, GIT_PREFIX) from the inherited
	// environment: for a client that works on ANOTHER worktree than the one this process
	// was started in (a hook exports them for its own).
	OwnRepo bool
}

// repoEnv are the variables OwnRepo drops.
var repoEnv = []string{"GIT_DIR=", "GIT_WORK_TREE=", "GIT_INDEX_FILE=", "GIT_COMMON_DIR=", "GIT_PREFIX="}

// Result is the outcome of one git call.
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Error is returned when git exits non-zero or cannot start.
type Error struct {
	Args     []string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.Args, " "), e.ExitCode, msg)
}

func (e *Error) Unwrap() error { return e.Err }

// Read runs a git command that must not write anything: GIT_OPTIONAL_LOCKS=0.
func (c Client) Read(ctx context.Context, args ...string) (Result, error) {
	return c.run(ctx, nil, true, args)
}

// Write runs a git command that may change repository state.
func (c Client) Write(ctx context.Context, args ...string) (Result, error) {
	return c.run(ctx, nil, false, args)
}

// WriteStdin is Write with data piped to git's stdin.
func (c Client) WriteStdin(ctx context.Context, stdin []byte, args ...string) (Result, error) {
	return c.run(ctx, stdin, false, args)
}

// ReadStdin is Read with data piped to git's stdin.
func (c Client) ReadStdin(ctx context.Context, stdin []byte, args ...string) (Result, error) {
	return c.run(ctx, stdin, true, args)
}

func (c Client) run(ctx context.Context, stdin []byte, read bool, args []string) (Result, error) {
	calls.Add(1)
	trace(args)

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.Dir
	cmd.Env = os.Environ()
	if c.OwnRepo {
		cmd.Env = nil
		for _, kv := range os.Environ() {
			keep := true
			for _, p := range repoEnv {
				keep = keep && !strings.HasPrefix(kv, p)
			}
			if keep {
				cmd.Env = append(cmd.Env, kv)
			}
		}
	}
	cmd.Env = append(cmd.Env, c.Env...)
	if read {
		cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	err := cmd.Run()
	res := Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return res, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
	} else {
		res.ExitCode = -1
	}
	return res, &Error{Args: args, ExitCode: res.ExitCode, Stderr: stderr.String(), Err: err}
}

// trace appends a line to the file named by TraceEnv. Best effort: a tracing
// failure must never change behaviour.
func trace(args []string) {
	path := os.Getenv(TraceEnv)
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line := strings.ReplaceAll(strings.Join(args, " "), "\n", `\n`) // one line per call
	fmt.Fprintf(f, "%d git %s\n", time.Now().UnixNano(), line)
}
