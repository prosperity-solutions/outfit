package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/hooks"
	"github.com/prosperity-solutions/outfit/internal/repo"
)

// cmdHook is what every shim runs: `outfit hook <name> <git's args…>`. It is
// plumbing, never run by a person, and its contract is strict:
//
//   - the team's original hook runs FIRST, with git's args, stdin and
//     environment, and ITS exit code is ours, exactly: outfit's own result never
//     changes it (a hook that fails is the end of the story);
//   - `hook.<name>=off` (resolved: personal overrides lose to locks) skips that
//     original hook entirely;
//   - on pre-commit the guard runs AFTER the team hook succeeded, so it also
//     sees what a formatter hook staged (lint-staged `git add`s swapped files);
//   - any internal failure fails toward RUNNING the team's hook, never toward
//     silently skipping it (a secret scan must not vanish because a config read
//     failed); the guard, which protects history, fails closed instead.
func cmdHook(ctx context.Context, e Env, args []string) int {
	if len(args) == 0 || !hooks.IsKnown(args[0]) {
		return report(e.Stdout, e.Stderr, false, Usagef("usage: outfit hook <git-hook-name> [args…] (run by git, not by hand)"))
	}
	name, rest := args[0], args[1:]
	if e.Getenv(hooks.ActiveEnv) == name {
		fmt.Fprintf(e.Stderr, "outfit: the %s hook chains back into outfit (a team hook that is a symlink to a shim, or a core.hooksPath that loops): not running it again\n", name)
		return 0
	}
	stdin := e.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	r, derr := repo.Discover(e.Dir, e.Getenv)
	if derr != nil || r.WorkTree == "" {
		// Not somewhere we understand. Never skip silently: the best a hook can
		// do from here is the repository's own hooks directory.
		fmt.Fprintf(e.Stderr, "outfit: cannot locate the repository (%v); running the team's %s hook from $GIT_DIR/hooks if there is one\n", derr, name)
		if gd := e.Getenv("GIT_DIR"); gd != "" {
			code, _ := hooks.Chain(ctx, filepath.Join(absFrom(e.Dir, gd), "hooks"), name, rest, stdin, e.Stdout, e.Stderr)
			return code
		}
		return 0
	}
	home := e.Getenv("HOME")

	l, lerr := load(ctx, e)
	var gc config.GitConfig
	g := gitx.Client{Dir: r.WorkTree}
	var origDir string
	if lerr == nil {
		gc, g = l.Cfg, l.Git
		origDir = hooks.OriginalDir(gc, hooks.Dir(r.CommonDir), r.WorkTree, r.CommonDir, home)
	} else {
		fmt.Fprintf(e.Stderr, "outfit: %v; running the team's %s hook anyway\n", lerr, name)
		origDir = filepath.Join(r.CommonDir, "hooks")
		if p := hooks.HooksPathFromFiles(filepath.Join(r.CommonDir, "config"), globalGitConfig(e)); p != "" {
			origDir = hooks.ExpandPath(p, r.WorkTree, home)
		}
	}

	// A hook switched off for this profile or machine (and not locked on) is skipped.
	skip := false
	if lerr == nil {
		if t, ok := l.Result.Get(config.HookPrefix + name); ok && t.Value == "off" {
			skip = true
		}
	}
	if !skip {
		if code, _ := hooks.Chain(ctx, origDir, name, rest, stdin, e.Stdout, e.Stderr); code != 0 {
			return code
		}
	}

	if name == "pre-commit" {
		vs, gerr := hooks.Guard(ctx, g, r.GitDir, gc, lerr == nil, e.Getenv)
		if gerr != nil {
			vs = append(vs, hooks.Violation{Message: "the pre-commit guard could not run: " + gerr.Error(), Fix: "outfit status"})
		}
		if len(vs) > 0 {
			fmt.Fprintln(e.Stderr, "outfit: commit refused:")
			for _, v := range vs {
				fmt.Fprintf(e.Stderr, "  %s\n", v)
			}
			return 1
		}
	}
	return 0
}

func absFrom(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// globalGitConfig is the user-level git config file, for reading core.hooksPath
// when `git config --list` itself failed.
func globalGitConfig(e Env) string {
	if p := e.Getenv("GIT_CONFIG_GLOBAL"); p != "" {
		return p
	}
	return filepath.Join(e.Getenv("HOME"), ".gitconfig")
}
