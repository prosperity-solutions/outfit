package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/agents"
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
	agentsFlag := fs.Bool("agents", false, "also add a Claude Code SessionStart entry (.claude/settings.local.json) that runs `outfit sync`")
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
	// A Claude Code SessionStart entry, in each worktree that has a .claude directory.
	var agentsOut *agentsJSON
	if *agentsFlag {
		agentsOut = &agentsJSON{Added: []string{}, NoClaude: []string{}, Errors: []string{}}
		for _, w := range hooks.Worktrees(l.Repo.CommonDir) {
			agentsOut.worktrees++
			switch {
			case !agents.HasClaude(w.Path):
				agentsOut.NoClaude = append(agentsOut.NoClaude, w.Path)
			case *dry:
				if would, aerr := agents.WouldInstall(w.Path, e.Exe); aerr != nil {
					agentsOut.Errors = append(agentsOut.Errors, aerr.Error())
				} else if would {
					agentsOut.Added = append(agentsOut.Added, w.Path)
				}
			case settingsTracked(ctx, l, w.Path):
				// A repository that commits the file makes it everyone's: editing it would
				// put this machine's binary path and a sync into every teammate's session.
				agentsOut.Errors = append(agentsOut.Errors, w.Path+"/"+agents.SettingsFile+" is tracked by git, so outfit does not edit it (it is meant to be personal)")
			default:
				if changed, aerr := agents.Install(w.Path, e.Exe); aerr != nil {
					agentsOut.Errors = append(agentsOut.Errors, aerr.Error())
				} else if changed {
					agentsOut.Added = append(agentsOut.Added, w.Path)
					ignoreSettings(ctx, l, w.Path)
				}
			}
		}
	}
	if *asJSON {
		out := struct {
			*hooks.InstallResult
			Agents    *agentsJSON `json:"agents,omitempty"`
			Sync      *syncJSON   `json:"sync,omitempty"`
			SyncError string      `json:"sync_error,omitempty"`
			Fix       string      `json:"fix,omitempty"`
		}{InstallResult: res, Agents: agentsOut}
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
		return initExit(syncRes, syncErr, agentsOut)
	}
	if syncErr != nil {
		fmt.Fprintf(e.Stderr, "outfit: profile files were not set up: %v\n  fix: outfit sync\n", syncErr)
	} else if syncRes != nil {
		printNotes(e, syncRes)
	}
	if agentsOut != nil {
		printAgents(e, agentsOut, *dry)
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
	return initExit(syncRes, syncErr, agentsOut)
}

// ignoreSettings keeps the personal settings file out of `git add -A`, unless git
// already ignores it (a global ignore, Claude Code's own entry).
func ignoreSettings(ctx context.Context, l *loaded, worktree string) {
	g := l.Git
	g.Dir = worktree
	if _, err := g.Read(ctx, "check-ignore", "-q", "--", agents.SettingsFile); err == nil {
		return
	}
	_ = apply.AddExcludeEntry(filepath.Join(l.Repo.CommonDir, "info", "exclude"), agents.IgnoreComment, agents.IgnoreLine)
}

// settingsTracked reports whether git tracks the worktree's personal settings file.
func settingsTracked(ctx context.Context, l *loaded, worktree string) bool {
	g := l.Git
	g.Dir = worktree
	res, err := g.Read(ctx, "--literal-pathspecs", "ls-files", "-z", "--", agents.SettingsFile)
	return err == nil && len(res.Stdout) > 0
}

// agentsJSON is what `init --agents` did in each worktree.
type agentsJSON struct {
	Added    []string `json:"added"`
	NoClaude []string `json:"no_claude_dir"`
	Errors   []string `json:"errors"`

	worktrees int // how many worktrees were looked at
}

func printAgents(e Env, a *agentsJSON, dry bool) {
	verb := "added"
	if dry {
		verb = "would add"
	}
	if len(a.Added) > 0 {
		fmt.Fprintf(e.Stdout, "Claude Code SessionStart entry %s in %d worktree(s) (%s)\n", verb, len(a.Added), agents.SettingsFile)
	}
	if len(a.NoClaude) > 0 && len(a.Added) == 0 && len(a.Errors) == 0 && len(a.NoClaude) == a.worktrees {
		fmt.Fprintln(e.Stdout, "no .claude directory here, so no SessionStart entry (create it, then run: outfit init --agents)")
	}
	for _, er := range a.Errors {
		fmt.Fprintf(e.Stderr, "outfit: could not add the SessionStart entry: %s\n", er)
	}
}

// initExit is 0 when the dispatcher is in and the profile files are in order, and
// 3 when the dispatcher is installed but a person has to look at the profile files
// (a sparse checkout that is not ours, a refusal, an error): init's own job is done
// either way, and rerunning it is harmless.
func initExit(syncRes *apply.Result, syncErr error, a *agentsJSON) int {
	if syncErr != nil || (syncRes != nil && syncExit(syncRes) != ExitOK) || (a != nil && len(a.Errors) > 0) {
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
	for _, w := range hooks.Worktrees(l.Repo.CommonDir) {
		if changed, aerr := agents.Remove(w.Path); aerr != nil {
			undoFailed = append(undoFailed, aerr.Error())
		} else if changed {
			undone = append(undone, "Claude Code SessionStart entry in "+w.Path)
		}
	}
	if removed, rerr := apply.RemoveExcludeEntry(filepath.Join(l.Repo.CommonDir, "info", "exclude"), agents.IgnoreComment, agents.IgnoreLine); rerr != nil {
		undoFailed = append(undoFailed, "info/exclude: "+rerr.Error())
	} else if removed {
		undone = append(undone, ".git/info/exclude entry for the Claude Code settings")
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
