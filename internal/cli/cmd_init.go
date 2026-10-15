package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/apply"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/hooks"
)

// cmdInit installs the hook dispatcher: shims, per-worktree config and
// core.hooksPath in every worktree. Idempotent; --dry-run writes nothing.
func cmdInit(ctx context.Context, e Env, args []string) int {
	fs := newFlags("init")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	dry := fs.Bool("dry-run", false, "show what would change and write nothing")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "init", pos, args); bad {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }

	gv, err := (gitx.Client{Dir: e.Dir}).RequireFloor(ctx)
	if err != nil {
		return fail(Errorf("%v", err))
	}
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	if e.Exe == "" {
		return fail(Errorf("cannot determine the path of the running outfit binary"))
	}
	res, err := hooks.Install(ctx, l.Git, l.Repo, l.Cfg, hooks.InstallOptions{
		DryRun: *dry, Binary: e.Exe, Version: e.Build.Version, GitVersion: gv.String(), Home: e.Getenv("HOME"),
	})
	if err != nil {
		return fail(Errorf("%v", err))
	}
	// Profile files: with committed profiles the inactive ones are hidden from disk
	// from the first moment, and .git/info/exclude keeps search tools out of
	// .outfit. A sparse checkout that is not ours is refused, not fatal: the
	// dispatcher is installed either way.
	var syncRes *apply.Result
	var syncErr error
	if !*dry {
		if l2, lerr := load(ctx, e); lerr != nil {
			syncErr = lerr
		} else {
			syncRes, _, syncErr = converge(ctx, e, l2, true)
		}
	}
	if *asJSON {
		out := struct {
			*hooks.InstallResult
			Sync      *syncJSON `json:"sync,omitempty"`
			SyncError string    `json:"sync_error,omitempty"`
			Fix       string    `json:"fix,omitempty"`
		}{InstallResult: res}
		if syncRes != nil {
			j := toJSON(syncRes)
			out.Sync = &j
		}
		if syncErr != nil {
			out.SyncError = syncErr.Error()
			out.Fix = "outfit sync"
		} else if syncRes != nil && (syncRes.NeedsHuman || len(syncRes.Refused) > 0) {
			out.Fix = "outfit status"
		}
		b, _ := json.Marshal(out)
		fmt.Fprintln(e.Stdout, string(b))
		return initExit(syncRes, syncErr)
	}
	if syncErr != nil {
		fmt.Fprintf(e.Stderr, "outfit: profile files were not set up: %v\n  fix: outfit sync\n", syncErr)
	} else if syncRes != nil {
		printNotes(e, syncRes)
	}
	verb := map[bool]string{true: "would install", false: "installed"}[*dry]
	switch {
	case !res.Changed:
		fmt.Fprintf(e.Stdout, "already installed: %d hook shims in %s (nothing to do)\n", len(res.Shims), res.HooksDir)
	default:
		fmt.Fprintf(e.Stdout, "%s: %d hook shims in %s\n", verb, len(res.Shims), res.HooksDir)
		fmt.Fprintf(e.Stdout, "  your hooks keep running from %s\n", res.OriginalDir)
		if res.HooksPathChanged {
			fmt.Fprintf(e.Stdout, "  core.hooksPath set (worktree scope) in %d worktree(s)\n", len(res.Worktrees))
		}
		if res.EnabledWorktreeConfig {
			fmt.Fprintln(e.Stdout, "  extensions.worktreeConfig enabled")
		}
		if len(res.Removed) > 0 {
			fmt.Fprintf(e.Stdout, "  removed stale shims: %s\n", strings.Join(res.Removed, ", "))
		}
		if len(res.Adopted) > 0 {
			fmt.Fprintf(e.Stdout, "  moved hooks another tool installed into outfit's directory to %s: %s\n", res.OriginalDir, strings.Join(res.Adopted, ", "))
		}
		fmt.Fprintln(e.Stdout, "  to skip a hook on this machine: outfit set hook.<name>=off --global   (e.g. hook.pre-commit)")
		if len(res.MissingWorktrees) > 0 {
			fmt.Fprintf(e.Stdout, "  worktrees whose directory is gone (configured anyway; `git worktree prune` removes them): %s\n", strings.Join(res.MissingWorktrees, ", "))
		}
	}
	return initExit(syncRes, syncErr)
}

// initExit is 0 when the dispatcher is in and the profile files are in order, and
// 3 when the dispatcher is installed but a person has to look at the profile files
// (a sparse checkout that is not ours, a refusal, an error): init's own job is done
// either way, and rerunning it is harmless.
func initExit(syncRes *apply.Result, syncErr error) int {
	if syncErr != nil || (syncRes != nil && syncExit(syncRes) != ExitOK) {
		return ExitNeedsHuman
	}
	return ExitOK
}

// cmdUninstall undoes init everywhere.
func cmdUninstall(ctx context.Context, e Env, args []string) int {
	fs := newFlags("uninstall")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "uninstall", pos, args); bad {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	// Links go and base files come back BEFORE anything else is undone: sparse
	// checkout must still be on while they do, and disabled only after.
	var undone, undoFailed []string
	for _, w := range hooks.Worktrees(l.Repo.CommonDir) {
		did, failed := apply.Teardown(ctx, apply.Workspace{WorkTree: w.Path, GitDir: w.GitDir, CommonDir: l.Repo.CommonDir})
		undone = append(undone, did...)
		undoFailed = append(undoFailed, failed...)
	}
	if removed, err := apply.RemoveExclude(filepath.Join(l.Repo.CommonDir, "info", "exclude")); err != nil {
		undoFailed = append(undoFailed, "info/exclude: "+err.Error())
	} else if removed {
		undone = append(undone, ".git/info/exclude entry")
	}
	res, err := hooks.Uninstall(ctx, l.Git, l.Repo, l.Cfg, e.Getenv("HOME"))
	if err != nil {
		return fail(Errorf("%v", err))
	}
	if len(undone) > 0 {
		res.Changed = true
		res.Removed = append(res.Removed, undone...)
	}
	res.Failed = append(res.Failed, undoFailed...)
	if *asJSON {
		out, _ := json.Marshal(res)
		fmt.Fprintln(e.Stdout, string(out))
		if len(res.Failed) > 0 {
			return ExitError
		}
		return ExitOK
	}
	for _, f := range res.Failed {
		fmt.Fprintf(e.Stderr, "outfit: could not undo %s\n", f)
	}
	if len(res.Failed) > 0 {
		fmt.Fprintln(e.Stderr, "fix: resolve the above, then rerun `outfit uninstall` (it continues where it stopped)")
		return ExitError
	}
	if !res.Changed {
		fmt.Fprintln(e.Stdout, "nothing to uninstall")
		return ExitOK
	}
	var shims, state, cfgs int
	for _, r := range res.Removed {
		switch {
		case strings.HasPrefix(r, "shim "):
			shims++
		case strings.HasPrefix(r, "state "), r == "install record":
			state++
		default:
			cfgs++
		}
	}
	fmt.Fprintf(e.Stdout, "uninstalled: %d shims, %d config entries, %d state files removed\n", shims, cfgs, state)
	if len(res.Adopted) > 0 {
		fmt.Fprintf(e.Stdout, "  moved hooks another tool had installed back to the team's hooks directory: %s\n", strings.Join(res.Adopted, ", "))
	}
	return ExitOK
}
