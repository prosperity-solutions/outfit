package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/agents"
	"github.com/prosperity-solutions/outfit/internal/apply"
	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/hooks"
	"github.com/prosperity-solutions/outfit/internal/profiles"
	"github.com/prosperity-solutions/outfit/internal/state"
)

type statusFile struct {
	Path    string `json:"path"`
	Profile string `json:"profile"`
	Tier    string `json:"tier"`
}

type dispatcherInfo struct {
	Installed bool   `json:"installed"`
	OK        bool   `json:"ok"`
	Problem   string `json:"problem,omitempty"`
}

type statusJSON struct {
	Profile config.ProfileChoice `json:"profile"`
	// Tier is the active profile's tier ("" for base or a missing profile).
	Tier    string          `json:"tier"`
	Toggles []config.Toggle `json:"toggles"`
	// Dispatcher is the state of the hook dispatcher.
	Dispatcher dispatcherInfo `json:"dispatcher"`
	// AgentsSessionStart says this worktree has outfit's Claude Code SessionStart
	// entry (`outfit init --agents`).
	AgentsSessionStart bool         `json:"agents_session_start"`
	Locked             []string     `json:"locked"`
	Files              []statusFile `json:"files"`
	// Sync says whether the worktree matches the active profile (only when outfit
	// is installed here).
	Sync     *syncJSON        `json:"sync,omitempty"`
	Warnings []config.Warning `json:"warnings"`
}

// cmdStatus shows the active profile and where every value comes from. It is
// one of the commands that enforce the git floor (init, use and status; version
// only warns), and it never runs inside a hook.
func cmdStatus(ctx context.Context, e Env, args []string) int {
	fs := newFlags("status")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "status", pos, args); bad {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }

	if _, err := (gitx.Client{Dir: e.Dir}).RequireFloor(ctx); err != nil {
		return fail(Errorf("%v", err))
	}
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	res := l.Result

	st := statusJSON{Profile: res.Profile, Locked: nonNilStrings(res.Locked), Files: []statusFile{}, Warnings: nonNil(res.Warnings)}
	for _, k := range res.SortedKeys() {
		st.Toggles = append(st.Toggles, res.Toggles[k])
	}
	// A locked key the team never gave a value still deserves a line: personal
	// overrides of it are ignored, and it stays unset.
	for _, k := range res.Locked {
		if _, ok := res.Toggles[k]; !ok {
			st.Toggles = append(st.Toggles, config.Toggle{Key: k, Source: config.SourceTeam, Locked: true, Unset: true, Shadowed: []config.Candidate{}})
		}
	}
	sort.Slice(st.Toggles, func(i, j int) bool { return st.Toggles[i].Key < st.Toggles[j].Key })
	if st.Toggles == nil {
		st.Toggles = []config.Toggle{}
	}
	if l.Profile != nil {
		st.Tier = string(l.Profile.Tier)
		files := profiles.Files(*l.Profile)
		// Where outfit has synced this profile, list the swaps that really happened: a
		// path it skipped (not tracked, no file in the profile, an executable-bit
		// mismatch) is not one. Before the first sync, the profile's own files.
		if rec, err := state.Read(l.Repo.GitDir); err == nil && rec.Profile == l.Profile.Name {
			files = files[:0:0]
			for _, s := range rec.Swaps {
				files = append(files, s.Path)
			}
			sort.Strings(files)
		}
		for _, f := range files {
			st.Files = append(st.Files, statusFile{Path: f, Profile: l.Profile.Name, Tier: st.Tier})
		}
	}
	st.Dispatcher = dispatcherHealth(ctx, l, e.Getenv("HOME"))
	st.AgentsSessionStart = agents.Installed(l.Repo.WorkTree)
	var outOfSync, stale bool
	if st.Dispatcher.Installed && !st.Dispatcher.OK {
		outOfSync = true
		st.Warnings = append(st.Warnings, config.Warning{Source: "hook dispatcher", Message: st.Dispatcher.Problem + ": your team's hooks are NOT running. Fix: outfit init"})
	}
	if w := untrackedProfileWarning(ctx, l); w != "" {
		st.Warnings = append(st.Warnings, config.Warning{Source: "profile", Message: w})
	}
	// A clash between a personal and a committed profile name is worth saying.
	st.Warnings = append(st.Warnings, profiles.List(ctx, l.Git, l.Repo).Warnings...)

	exit := ExitOK
	var syncRes *apply.Result
	if st.Dispatcher.Installed {
		if sr, serr := apply.Check(ctx, syncInput(ctx, e, l, true)); serr != nil {
			st.Warnings = append(st.Warnings, config.Warning{Source: "sync", Message: "cannot tell whether the files are in sync: " + serr.Error()})
		} else {
			syncRes = sr
			j := toJSON(sr)
			j.Changed = false
			st.Sync = &j
			switch {
			case sr.NeedsHuman || len(sr.Refused) > 0:
				exit = ExitNeedsHuman
			case sr.Pending():
				outOfSync = true
			case len(sr.Stale()) > 0:
				stale = true
			}
		}
	}
	if (outOfSync || stale) && exit == ExitOK {
		exit = ExitOutOfSync
	}
	if *asJSON {
		out, _ := json.Marshal(st)
		fmt.Fprintln(e.Stdout, string(out))
		return exit
	}

	w := e.Stdout
	line := esc(res.Profile.Name)
	var notes []string
	if res.Profile.Name != config.Base && l.Profile == nil {
		notes = append(notes, "not found: behaving as base")
	}
	if st.Tier != "" {
		notes = append(notes, st.Tier)
	}
	switch res.Profile.Source {
	case config.SourceWorktree:
		notes = append(notes, "chosen for this worktree")
	case config.SourceLocal:
		notes = append(notes, "chosen for this clone")
	}
	if res.Profile.CloneDefault != "" {
		notes = append(notes, "clone default: "+res.Profile.CloneDefault)
	}
	if len(notes) > 0 {
		line += " (" + strings.Join(notes, "; ") + ")"
	}
	fmt.Fprintf(w, "profile  %s\n", line)

	if len(st.Toggles) == 0 {
		fmt.Fprintln(w, "toggles  (none)")
	}
	for i, t := range st.Toggles {
		label := "         "
		if i == 0 {
			label = "toggles  "
		}
		src := t.Source
		if t.Locked {
			src = "locked by team"
		}
		val := show(t.Value)
		if t.Unset {
			val = "(unset)"
		}
		fmt.Fprintf(w, "%s%s=%s (%s)\n", label, t.Key, val, src)
	}
	if len(st.Files) == 0 {
		fmt.Fprintln(w, "files    (no swaps)")
	}
	for i, f := range st.Files {
		label := "         "
		if i == 0 {
			label = "files    "
		}
		fmt.Fprintf(w, "%s%s ← %s (%s)\n", label, f.Path, f.Profile, f.Tier)
	}
	if st.Dispatcher.Installed {
		if st.Dispatcher.OK {
			fmt.Fprintln(w, "hooks    dispatcher installed")
		} else {
			fmt.Fprintf(w, "hooks    dispatcher BROKEN: %s\n", st.Dispatcher.Problem)
		}
	}
	if st.AgentsSessionStart {
		fmt.Fprintln(w, "agents   Claude Code SessionStart entry installed ("+agents.SettingsFile+")")
	}
	if syncRes != nil {
		switch {
		case syncRes.NeedsHuman || len(syncRes.Refused) > 0:
			fmt.Fprintln(w, "state    NEEDS A PERSON (see below)")
		case syncRes.Pending():
			fmt.Fprintln(w, "state    out of sync: run outfit sync")
		case len(syncRes.Stale()) > 0:
			fmt.Fprintf(w, "state    stale: the base differs from when it was swapped under %d file(s) (outfit diff <path>, outfit ack <path>)\n", len(syncRes.Stale()))
		default:
			fmt.Fprintln(w, "state    in sync")
		}
		for _, n := range syncRes.Notes {
			fmt.Fprintf(e.Stderr, "note: %s\n", n)
		}
		for _, a := range syncRes.Refused {
			fmt.Fprintf(e.Stderr, "note: %s\n", a.Reason)
			if a.Fix != "" {
				fmt.Fprintf(e.Stderr, "  fix: %s\n", a.Fix)
			}
		}
	}
	for _, wn := range st.Warnings {
		fmt.Fprintf(e.Stderr, "warning: %s\n", wn)
	}
	return exit
}

// dispatcherHealth checks that, if init ran, this worktree still points at
// outfit's shims and they are all intact. A moved repository or a deleted
// .git/outfit leaves a dangling core.hooksPath, which makes git skip EVERY hook
// without a word; a tool that renamed our pre-commit shim bypasses the guard.
func dispatcherHealth(ctx context.Context, l *loaded, home string) dispatcherInfo {
	ours := hooks.Dir(l.Repo.CommonDir)
	var got string
	for _, h := range l.Cfg.HooksPaths {
		if h.Scope == config.ScopeWorktree {
			got = h.Value
		}
	}
	_, installErr := state.ReadInstall(l.Repo.CommonDir)
	abs := ""
	if got != "" {
		abs = hooks.ExpandPath(got, l.Repo.WorkTree, home)
	}
	// Installed: init left its record, or the worktree still points at an outfit
	// directory (possibly one that is gone: exactly what a lost record looks like).
	if installErr != nil && (abs == "" || !hooks.IsOutfitDir(abs)) {
		return dispatcherInfo{}
	}
	info := dispatcherInfo{Installed: true}
	switch {
	case got == "":
		info.Problem = "core.hooksPath is not set for this worktree"
	case !hooks.SameDir(abs, ours):
		info.Problem = "core.hooksPath points at " + got + " (outfit's shims are in " + ours + "; was the repository moved?)"
	case len(hooks.Generated(ours)) == 0:
		info.Problem = "the shim directory " + ours + " is empty or missing"
	default:
		for _, n := range hooks.Triggers {
			if !hooks.IsOurs(filepath.Join(ours, n)) {
				info.Problem = "the " + n + " shim was replaced or removed (lefthook install does this)"
				break
			}
		}
		if info.Problem == "" {
			if f := hooks.Foreign(ours); len(f) > 0 {
				info.Problem = "another tool put hooks into outfit's directory: " + strings.Join(f, ", ")
			}
		}
		if info.Problem == "" {
			orig := hooks.OriginalDir(l.Cfg, ours, l.Repo.WorkTree, l.Repo.CommonDir, home)
			for _, n := range hooks.ShimNames(orig) {
				if !hooks.IsOurs(filepath.Join(ours, n)) {
					info.Problem = "the team has a " + n + " hook with no shim"
					break
				}
			}
		}
	}
	info.OK = info.Problem == ""
	return info
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// esc makes untrusted text safe to print: no raw control characters.
func esc(s string) string { return strings.Trim(strconv.Quote(s), `"`) }

// show prints a value verbatim when it is plain, quoted otherwise, so control
// characters in a value can never reach the terminal.
func show(v string) string {
	if q := strconv.Quote(v); v != "" && !strings.ContainsAny(v, " \"\\") && q == `"`+v+`"` {
		return v
	}
	return strconv.Quote(v)
}
