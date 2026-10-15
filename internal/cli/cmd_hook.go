package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/agents"
	"github.com/prosperity-solutions/outfit/internal/apply"
	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/hooks"
	"github.com/prosperity-solutions/outfit/internal/repo"
	"github.com/prosperity-solutions/outfit/internal/state"
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
	if len(args) == 0 {
		return report(e.Stdout, e.Stderr, false, Usagef("usage: outfit hook <git-hook-name> [args…] (run by git, not by hand)"))
	}
	if !hooks.IsKnown(args[0]) {
		return report(e.Stdout, e.Stderr, false, Usagef("%q is not a git hook name; git's hooks are: %s", args[0], strings.Join(hooks.Known, ", ")))
	}
	name, rest := args[0], args[1:]
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
			code, _ := hooks.Chain(ctx, filepath.Join(absFrom(e.Dir, gd), "hooks"), name, name+"@", rest, stdin, e.Stdout, e.Stderr)
			return code
		}
		return 0
	}
	home := e.Getenv("HOME")
	active := hooks.ActiveKey(name, r.CommonDir)
	if e.Getenv(hooks.ActiveEnv) == active {
		fmt.Fprintf(e.Stderr, "outfit: the %s hook chains back into outfit (a team hook that is a symlink to a shim, or a core.hooksPath that loops): not running it again\n", name)
		return 0
	}

	// `git worktree add` fires post-checkout in the NEW worktree with an all-zero old
	// sha, after copying the main worktree's whole config.worktree into it: that copy
	// carries `outfit.*` personal settings (a pinned profile) that were never chosen for
	// this worktree. Strip them first, so it follows the clone's default until
	// `outfit use --here` says otherwise. The copied hooksPath and sparse settings stay.
	// Only in a worktree outfit has never synced (no state.json): a deliberate pin made
	// here with `use --here` has one, so a later all-zero checkout (an orphan branch
	// switch) cannot take it away.
	if name == "post-checkout" && len(rest) >= 3 && allZeros(rest[0]) && rest[2] == "1" && r.GitDir != r.CommonDir && neverSynced(r.GitDir) {
		stripCopiedWorktreeKeys(ctx, e, r)
		agentsInNewWorktree(ctx, e, r)
	}

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
		if p := hooks.HooksPathFromFiles(filepath.Join(r.CommonDir, "config"), globalGitConfig(e), xdgGitConfig(e), "/etc/gitconfig"); p != "" {
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
		if code, _ := hooks.Chain(ctx, origDir, name, active, rest, stdin, e.Stdout, e.Stderr); code != 0 {
			return code
		}
	}

	switch name {
	case "post-checkout", "post-merge", "post-rewrite":
		// Keep the swapped files in step with what git just did (a checkout, a pull,
		// a rebase). Best effort: outfit's own result never changes a hook's exit code.
		if lerr == nil && e.Getenv(syncActiveEnv) == "" {
			// post-rewrite fires after a rebase has ended, but also after every `git commit --amend`,
			// which an open rebase (stopped at an `edit`) allows: only the first is "over".
			e.opDone = name == "post-rewrite" && len(rest) > 0 && rest[0] == "rebase"
			if res, _, serr := converge(ctx, e, l, false); serr != nil {
				fmt.Fprintf(e.Stderr, "outfit: sync after %s failed: %v\n", name, serr)
			} else {
				printNotes(e, res)
			}
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

// neverSynced reports a worktree git dir without outfit's state.json.
func neverSynced(gitDir string) bool {
	_, err := os.Lstat(state.Path(gitDir))
	return errors.Is(err, fs.ErrNotExist)
}

// agentsInNewWorktree gives a worktree added later the SessionStart entry the main
// worktree has (coding agents isolate their work in new worktrees), when it has a
// .claude directory and git does not track the personal settings file.
func agentsInNewWorktree(ctx context.Context, e Env, r repo.Repo) {
	wts := hooks.Worktrees(r.CommonDir)
	if len(wts) == 0 || wts[0].Path == "" || !agents.Installed(wts[0].Path) || !agents.HasClaude(r.WorkTree) {
		return
	}
	g := gitx.Client{Dir: r.WorkTree}
	if e.Getenv("GIT_DIR") != "" {
		g.Env = []string{"GIT_DIR=" + r.GitDir}
	}
	if res, err := g.Read(ctx, "--literal-pathspecs", "ls-files", "-z", "--", agents.SettingsFile); err != nil || len(res.Stdout) > 0 {
		return
	}
	if changed, err := agents.Install(r.WorkTree, e.Exe); err != nil || !changed {
		return
	}
	// Keep it out of `git add -A` here too (this branch may lack the ignore line).
	if _, err := g.Read(ctx, "check-ignore", "-q", "--", agents.SettingsFile); err != nil {
		_ = apply.AddExcludeEntry(filepath.Join(r.CommonDir, "info", "exclude"), agents.IgnoreComment, agents.IgnoreLine)
	}
}

// allZeros reports a git object id made only of zeros (the "null" old sha).
func allZeros(s string) bool {
	return s != "" && strings.Trim(s, "0") == ""
}

// stripCopiedWorktreeKeys unsets every outfit.* key the new worktree's
// config.worktree inherited. Best effort: a hook must not fail over it.
func stripCopiedWorktreeKeys(ctx context.Context, e Env, r repo.Repo) {
	g := gitx.Client{Dir: r.WorkTree}
	if e.Getenv("GIT_DIR") != "" {
		g.Env = []string{"GIT_DIR=" + r.GitDir}
	}
	res, err := g.Read(ctx, "config", "--worktree", "--name-only", "--list")
	if err != nil {
		return // no per-worktree config: nothing was copied
	}
	seen := map[string]bool{}
	for _, k := range strings.Split(string(res.Stdout), "\n") {
		k = strings.TrimSpace(k)
		if strings.HasPrefix(k, "outfit.") && !seen[k] {
			seen[k] = true
			_, _ = g.Write(ctx, "config", "--worktree", "--unset-all", k)
		}
	}
}

func absFrom(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// xdgGitConfig is $XDG_CONFIG_HOME/git/config (or ~/.config/git/config).
func xdgGitConfig(e Env) string {
	if x := e.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "git", "config")
	}
	return filepath.Join(e.Getenv("HOME"), ".config", "git", "config")
}

// globalGitConfig is the user-level git config file, for reading core.hooksPath
// when `git config --list` itself failed.
func globalGitConfig(e Env) string {
	if p := e.Getenv("GIT_CONFIG_GLOBAL"); p != "" {
		return p
	}
	return filepath.Join(e.Getenv("HOME"), ".gitconfig")
}
