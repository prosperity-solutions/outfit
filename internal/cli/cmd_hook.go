package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/hooks"
	"github.com/prosperity-solutions/outfit/internal/repo"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// cmdHook is what every shim runs: `outfit hook <name> <git's args…>`. It is
// plumbing, never run by a person, and its contract is strict:
//
//   - the team's original hook runs with git's args, stdin and environment, and
//     ITS exit code is ours, exactly: outfit's own result never changes it;
//   - `hook.<name>=off` (resolved: personal overrides lose to locks) skips that
//     original hook entirely;
//   - on pre-commit the guard runs first and may refuse the commit;
//   - any internal failure fails toward RUNNING the team's hook, never toward
//     silently skipping it (a secret scan must not vanish because a config read
//     failed).
func cmdHook(ctx context.Context, e Env, args []string) int {
	if len(args) == 0 || !hooks.IsKnown(args[0]) {
		return report(e.Stdout, e.Stderr, false, Usagef("usage: outfit hook <git-hook-name> [args…] (run by git, not by hand)"))
	}
	name, rest := args[0], args[1:]

	r, err := repo.Discover(e.Dir, e.Getenv)
	if err != nil || r.WorkTree == "" {
		return 0 // not somewhere we can do anything; never get in git's way
	}
	home := e.Getenv("HOME")
	cwd := r.WorkTree

	l, lerr := load(ctx, e)
	var gc config.GitConfig
	if lerr == nil {
		gc = l.Cfg
	} else {
		fmt.Fprintf(e.Stderr, "outfit: %v; running the team's %s hook anyway\n", lerr, name)
	}
	origDir, _ := hooks.OriginalDir(gc, hooks.Dir(r.CommonDir), cwd, r.CommonDir, home)

	if name == "pre-commit" && lerr == nil {
		st, serr := state.Read(r.GitDir)
		if serr != nil && serr != state.ErrNone {
			fmt.Fprintf(e.Stderr, "outfit: %v\n", serr)
		}
		vs, gerr := hooks.Guard(ctx, l.Git, st, r.GitDir, gc, e.Getenv)
		if gerr != nil {
			fmt.Fprintf(e.Stderr, "outfit: pre-commit guard could not run: %v\n", gerr)
		}
		if len(vs) > 0 {
			fmt.Fprintln(e.Stderr, "outfit: commit refused:")
			for _, v := range vs {
				fmt.Fprintf(e.Stderr, "  %s\n", v)
			}
			return 1
		}
	}

	if lerr == nil {
		if t, ok := l.Result.Get(config.HookPrefix + name); ok && t.Value == "off" {
			return 0 // switched off for this profile or machine (and not locked on)
		}
	}
	stdin := e.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	code, _ := hooks.Chain(ctx, origDir, name, rest, stdin, e.Stdout, e.Stderr, filepath.Clean(cwd))
	return code
}
