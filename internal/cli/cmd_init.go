package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/agents"
	"github.com/prosperity-solutions/outfit/internal/apply"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/hooks"
	"github.com/prosperity-solutions/outfit/internal/profiles"
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
		for _, w := range agentWorktrees(l) {
			agentsOut.worktrees++
			switch {
			case !agents.HasClaude(w.Path):
				agentsOut.NoClaude = append(agentsOut.NoClaude, w.Path)
			case settingsTracked(ctx, l, w.Path):
				// A repository that commits the file makes it everyone's: editing it would
				// put this machine's binary path and a sync into every teammate's session.
				agentsOut.Errors = append(agentsOut.Errors, w.Path+"/"+agents.SettingsFile+errTracked)
			case *dry:
				if would, aerr := agents.WouldInstall(w.Path, e.Exe); aerr != nil {
					agentsOut.Errors = append(agentsOut.Errors, aerr.Error())
				} else if would {
					agentsOut.Added = append(agentsOut.Added, w.Path)
				}
			default:
				changed, aerr := agents.Install(w.Path, e.Exe)
				if aerr != nil {
					agentsOut.Errors = append(agentsOut.Errors, aerr.Error())
					break
				}
				if changed {
					agentsOut.Added = append(agentsOut.Added, w.Path)
				}
				// Every run, not only when the entry was just written: an interrupted run
				// (or a branch without the ignore line) is repaired by running init again.
				if ierr := ignoreSettings(ctx, l, w.Path); ierr != nil {
					agentsOut.Errors = append(agentsOut.Errors, "cannot keep the settings file out of git add -A: "+ierr.Error())
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
func ignoreSettings(ctx context.Context, l *loaded, worktree string) error {
	g := l.Git
	g.Dir = worktree
	if _, err := g.Read(ctx, "check-ignore", "-q", "--", agents.SettingsFile); err == nil {
		return nil
	}
	return apply.AddExcludeEntry(filepath.Join(l.Repo.CommonDir, "info", "exclude"), agents.IgnoreComment, agents.IgnoreLine)
}

// agentWorktrees lists the worktrees whose paths outfit knows (never an empty path: that
// would resolve .claude against the current directory).
func agentWorktrees(l *loaded) []hooks.Worktree {
	var out []hooks.Worktree
	for _, w := range uninstallWorktrees(l) {
		if w.Path != "" {
			out = append(out, w.Worktree)
		}
	}
	return out
}

// settingsTracked reports whether git tracks the worktree's personal settings file.
func settingsTracked(ctx context.Context, l *loaded, worktree string) bool {
	g := l.Git
	g.Dir = worktree
	res, err := g.Read(ctx, "--literal-pathspecs", "ls-files", "-z", "--", agents.SettingsFile)
	return err == nil && len(res.Stdout) > 0
}

// Texts that are built in one place and recognised in another.
const (
	errTracked          = " is tracked by git, so outfit does not edit it (it is meant to be personal)"
	agentsRemovedPrefix = "Claude Code SessionStart entry in "
	excludeRemoved      = ".git/info/exclude entry for the Claude Code settings"
)

// uninstallWorktree is a worktree to undo (its path may be empty when nothing says where
// the main worktree of a separate git dir is: Teardown reports that).
type uninstallWorktree struct {
	hooks.Worktree
}

func (w uninstallWorktree) label() string {
	if w.Path != "" {
		return w.Path
	}
	return w.GitDir
}

// uninstallWorktrees is every worktree, with the main one's path filled in from the
// current directory's repository when git's own config does not say (and left empty,
// which Teardown reports, when nothing does).
func uninstallWorktrees(l *loaded) []uninstallWorktree {
	var out []uninstallWorktree
	for _, w := range hooks.Worktrees(l.Repo.CommonDir) {
		if w.Path == "" && w.Main && l.Repo.GitDir == l.Repo.CommonDir {
			w.Path = l.Repo.WorkTree
		}
		out = append(out, uninstallWorktree{w})
	}
	return out
}

// personalTierDirs lists the real tier directories of the personal profiles: the local
// one and each worktree's. A directory (or the outfit directory above it) that is a link
// is left out: reading or deleting through it would reach somewhere that is not outfit's.
func personalTierDirs(l *loaded) []string {
	var out []string
	add := func(d string) {
		if profiles.RealDir(d) && profiles.RealDir(filepath.Dir(d)) {
			out = append(out, d)
		}
	}
	add(profiles.TierDirIn("", l.Repo.CommonDir, l.Repo.GitDir, profiles.Local))
	for _, w := range hooks.Worktrees(l.Repo.CommonDir) {
		add(profiles.TierDirIn(w.Path, l.Repo.CommonDir, w.GitDir, profiles.Worktree))
	}
	return out
}

// personalProfiles lists the directories of the personal profiles (valid names only).
func personalProfiles(l *loaded) []string {
	var out []string
	for _, tierDir := range personalTierDirs(l) {
		ents, err := os.ReadDir(tierDir)
		if err != nil {
			continue
		}
		for _, en := range ents {
			if en.IsDir() && profiles.ValidName(en.Name()) == nil {
				out = append(out, filepath.Join(tierDir, en.Name()))
			}
		}
	}
	return out
}

// removeProfileDir deletes one profile directory (below its tier directory only).
func removeProfileDir(dir string) error {
	root, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	return root.RemoveAll(filepath.Base(dir))
}

// agentsJSON is what `init --agents` did in each worktree.
type agentsJSON struct {
	Added    []string `json:"added"`
	NoClaude []string `json:"no_claude_dir"`
	Errors   []string `json:"errors"`

	worktrees int // how many worktrees were looked at
}

func printAgents(e Env, a *agentsJSON, dry bool) {
	if len(a.Added) > 0 {
		if dry {
			fmt.Fprintf(e.Stdout, "would add the Claude Code SessionStart entry in %d worktree(s) (%s)\n", len(a.Added), agents.SettingsFile)
		} else {
			fmt.Fprintf(e.Stdout, "Claude Code SessionStart entry added in %d worktree(s) (%s)\n", len(a.Added), agents.SettingsFile)
		}
	}
	if len(a.NoClaude) > 0 && len(a.Added) == 0 && len(a.Errors) == 0 && len(a.NoClaude) == a.worktrees {
		fmt.Fprintln(e.Stdout, "no .claude directory here, so no SessionStart entry (create it, then run: outfit init --agents)")
	}
	for _, er := range a.Errors {
		fmt.Fprintf(e.Stderr, "outfit: could not add the SessionStart entry: %s\n", er)
		switch {
		case strings.HasSuffix(er, errTracked):
			fmt.Fprintln(e.Stderr, "  fix: git rm --cached "+agents.SettingsFile+"   (keep it personal), then: outfit init --agents")
		default:
			fmt.Fprintln(e.Stderr, "  fix: repair or remove "+agents.SettingsFile+" by hand (or add the hook yourself: outfit sync on SessionStart), then: outfit init --agents")
		}
	}
}

// initExit is 0 when the dispatcher is in and the profile files are in order, and
// 3 when the dispatcher is installed but a person has to look: at the profile files
// (a sparse checkout that is not ours, a refusal, an error) or at the SessionStart entry
// `--agents` asked for and could not write. init's own job is done either way, and
// rerunning it is harmless.
func initExit(syncRes *apply.Result, syncErr error, a *agentsJSON) int {
	if syncErr != nil || (syncRes != nil && syncExit(syncRes) != ExitOK) || (a != nil && len(a.Errors) > 0) {
		return ExitNeedsHuman
	}
	return ExitOK
}

// cmdUninstall undoes init everywhere.
func cmdUninstall(ctx context.Context, e Env, args []string) int {
	fs := newFlags("uninstall")
	purge := fs.Bool("purge", false, "also delete your local and worktree profiles (they are kept otherwise)")
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
	var tornDown, stopped []string
	for _, w := range uninstallWorktrees(l) {
		did, failed := apply.Teardown(ctx, apply.Workspace{WorkTree: w.Path, GitDir: w.GitDir, CommonDir: l.Repo.CommonDir})
		undone = append(undone, did...)
		for _, f := range failed {
			undoFailed = append(undoFailed, w.label()+": "+f)
		}
		if len(failed) > 0 {
			stopped = append(stopped, w.label())
		} else {
			tornDown = append(tornDown, w.label())
		}
	}
	// A link that could not go (or a regular file where one stood) stops here:
	// switching sparse checkout off over it would leave a ` T` status that `commit -a`
	// turns into a symlink in history, and removing state.json would forget which links
	// to retry. Everything else (hooks, config, profiles) stays as it is; the rerun
	// continues where this one stopped.
	if len(undoFailed) > 0 {
		partial := &hooks.UninstallResult{Changed: len(undone) > 0, Removed: nonNilStrings(undone), Adopted: []string{}, Failed: undoFailed}
		if *asJSON {
			out, _ := json.Marshal(partial)
			fmt.Fprintln(e.Stdout, string(out))
			return ExitError
		}
		for _, f := range undoFailed {
			fmt.Fprintf(e.Stderr, "outfit: could not undo %s\n", f)
		}
		if len(tornDown) > 0 {
			fmt.Fprintf(e.Stderr, "outfit: already undone in: %s; stopped in: %s\n", strings.Join(tornDown, ", "), strings.Join(stopped, ", "))
		}
		fmt.Fprintln(e.Stderr, "fix: resolve the above, then rerun `outfit uninstall` (the hooks, settings and your profiles are as they were; a worktree already undone is linked again by its next hook until the rerun finishes)")
		return ExitError
	}
	if removed, err := apply.RemoveExclude(filepath.Join(l.Repo.CommonDir, "info", "exclude")); err != nil {
		undoFailed = append(undoFailed, "info/exclude: "+err.Error())
	} else if removed {
		undone = append(undone, ".git/info/exclude entry")
	}
	agentsFailed := false
	for _, w := range agentWorktrees(l) {
		if changed, aerr := agents.Remove(w.Path); aerr != nil {
			undoFailed = append(undoFailed, aerr.Error())
			agentsFailed = true
		} else if changed {
			undone = append(undone, agentsRemovedPrefix+w.Path)
		}
	}
	// The exclude line stays while an entry is still there to be ignored: a rerun after
	// the file is fixed finishes the job.
	if !agentsFailed {
		if removed, rerr := apply.RemoveExcludeEntry(filepath.Join(l.Repo.CommonDir, "info", "exclude"), agents.IgnoreComment, agents.IgnoreLine); rerr != nil {
			undoFailed = append(undoFailed, "info/exclude: "+rerr.Error())
		} else if removed {
			undone = append(undone, excludeRemoved)
		}
	}
	// Local and worktree profiles are the person's own files, never versioned: they
	// survive unless --purge says otherwise.
	kept := personalProfiles(l)
	if *purge {
		for _, dir := range kept {
			if rerr := removeProfileDir(dir); rerr != nil {
				undoFailed = append(undoFailed, dir+": "+rerr.Error())
			} else {
				undone = append(undone, "personal profile "+dir)
			}
		}
		// The tier directories too, with anything somebody put there by hand (a name
		// outfit does not list): they hold nothing else.
		for _, td := range personalTierDirs(l) {
			if rerr := removeProfileDir(td); rerr != nil {
				undoFailed = append(undoFailed, td+": "+rerr.Error())
			}
		}
		kept = nil
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
		out, _ := json.Marshal(struct {
			*hooks.UninstallResult
			KeptProfiles []string `json:"kept_profiles"`
		}{res, nonNilStrings(kept)})
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
	defer func() {
		if len(kept) > 0 {
			var names []string
			dirs := map[string]bool{}
			for _, k := range kept {
				names = append(names, filepath.Base(k))
				dirs[filepath.Dir(k)] = true
			}
			var where []string
			for d := range dirs {
				where = append(where, d)
			}
			sort.Strings(where)
			fmt.Fprintf(e.Stdout, "  kept %d personal profile(s): %s (local and worktree profiles are yours; they live in %s; outfit uninstall --purge deletes them)\n", len(kept), strings.Join(names, ", "), strings.Join(where, ", "))
		}
	}()
	if !res.Changed {
		fmt.Fprintln(e.Stdout, "nothing to uninstall")
		return ExitOK
	}
	var shims, state, cfgs, personal int
	for _, r := range res.Removed {
		switch {
		case strings.HasPrefix(r, "personal profile "):
			personal++
		case strings.HasPrefix(r, "shim "):
			shims++
		case strings.HasPrefix(r, "state "), r == "install record":
			state++
		default:
			cfgs++
		}
	}
	fmt.Fprintf(e.Stdout, "uninstalled: %d shims, %d config entries, %d state files removed\n", shims, cfgs, state)
	if personal > 0 {
		fmt.Fprintf(e.Stdout, "  also removed %d personal profile(s) (--purge)\n", personal)
	}
	for _, r := range res.Removed {
		if strings.HasPrefix(r, agentsRemovedPrefix) || r == excludeRemoved {
			fmt.Fprintf(e.Stdout, "  also removed: %s\n", r)
		}
	}
	if len(res.Adopted) > 0 {
		fmt.Fprintf(e.Stdout, "  moved hooks another tool had installed back to the team's hooks directory: %s\n", strings.Join(res.Adopted, ", "))
	}
	return ExitOK
}
