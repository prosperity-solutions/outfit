package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/apply"
	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/profiles"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// syncActiveEnv is set on every git process a sync starts. A git command that
// itself fires a hook (checkout) must not make outfit sync again from inside that
// sync: the nested one would wait for the lock the outer one holds.
const syncActiveEnv = "OUTFIT_SYNC_ACTIVE"

// requireInstalled is the guard in front of everything that swaps files: without
// the dispatcher there is no pre-commit guard, and a swap nobody guards is how a
// symlink ends up in history.
func requireInstalled(l *loaded) error {
	if _, err := state.ReadInstall(l.Repo.CommonDir); err != nil {
		return Errorf("outfit is not installed in this repository yet (no hook dispatcher, so nothing would keep a swapped file out of a commit)").WithFix("outfit init")
	}
	return nil
}

// syncInput describes the repository to apply: the active profile, the files it
// swaps, whether committed profiles exist (so inactive ones are hidden), and author
// mode. useHead reads a committed profile's file list from HEAD, for the moment
// right after a switch when its files are not on disk yet.
func syncInput(ctx context.Context, e Env, l *loaded, useHead bool) apply.Input {
	r := l.Repo
	st, _ := state.Read(r.GitDir)
	in := apply.Input{
		W:       apply.Workspace{WorkTree: r.WorkTree, GitDir: r.GitDir, CommonDir: r.CommonDir},
		Git:     l.Git,
		Cfg:     l.Cfg,
		Profile: l.Profile,

		OperationDone: e.opDone,
	}
	// LC_ALL=C: sync recognises one git failure ("not up to date", a half-done
	// checkout) by its text, which a translated git would not print.
	in.Git.Env = append(append([]string(nil), l.Git.Env...), syncActiveEnv+"=1", "LC_ALL=C", "LANGUAGE=")
	if st != nil {
		in.Author = st.Author
		in.CommittedProfiles = st.CommittedProfiles
	}
	if !in.CommittedProfiles {
		in.CommittedProfiles = committedProfilesOnDisk(r.WorkTree)
	}
	if l.Profile != nil {
		// HEAD only while the profile's files are not on disk yet (right after a
		// switch). Once they are, the disk is the truth, uncommitted files included.
		if l.Profile.Tier == profiles.Committed && !profiles.RealDir(l.Profile.Dir) {
			in.Files = profiles.HeadFiles(ctx, l.Git, l.Profile.Name)
		} else {
			in.Files = profiles.Files(*l.Profile)
		}
	}
	if useHead && !in.CommittedProfiles {
		for _, p := range profiles.List(ctx, l.Git, r).Profiles {
			if p.Tier == profiles.Committed {
				in.CommittedProfiles = true
			}
		}
	}
	return in
}

// committedProfilesOnDisk reports whether .outfit/profiles has at least one
// profile directory (read without git).
func committedProfilesOnDisk(worktree string) bool {
	dir := filepath.Join(worktree, filepath.FromSlash(profiles.CommittedRoot))
	if !profiles.RealDir(dir) {
		return false
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, en := range ents {
		if en.IsDir() && profiles.ValidName(en.Name()) == nil {
			return true
		}
	}
	return false
}

// converge runs sync until it has nothing left to do (at most three passes): the
// first pass of a switch may only be able to bring the new profile's files onto
// disk, the second links them. After a change it loads the configuration again,
// since sync itself changes some of it. The last pass's result is returned, with
// "needs a person" remembered from any pass.
func converge(ctx context.Context, e Env, l *loaded, useHead bool) (*apply.Result, *loaded, error) {
	var res, earlier *apply.Result
	var err error
	for pass := 0; pass < 3; pass++ {
		in := syncInput(ctx, e, l, useHead)
		res, err = apply.Run(ctx, in)
		if err != nil {
			return res, l, err
		}
		res.Merge(earlier)
		earlier = res
		if len(res.Refused) > 0 || !res.Changed {
			break
		}
		if l, err = load(ctx, e); err != nil {
			return res, l, err
		}
	}
	return res, l, nil
}

type syncJSON struct {
	Changed    bool     `json:"changed"`
	InSync     bool     `json:"in_sync"`
	NeedsHuman bool     `json:"needs_human"`
	Notes      []string `json:"notes"`
	Refused    []string `json:"refused"`
	Fixes      []string `json:"fixes"`
	// Stale lists swapped paths whose base file differs from when it was swapped (`outfit diff`, `outfit ack`).
	Stale      []string `json:"stale"`
	Quarantine string   `json:"quarantine,omitempty"`
}

func toJSON(res *apply.Result) syncJSON {
	j := syncJSON{Changed: res.Changed, NeedsHuman: res.NeedsHuman || len(res.Refused) > 0, Notes: nonNilStrings(res.Notes),
		Refused: []string{}, Fixes: []string{}, Stale: []string{}, Quarantine: res.Quarantine, InSync: !res.Pending() && !res.NeedsHuman}
	for _, a := range res.Stale() {
		j.Stale = append(j.Stale, a.Path)
	}
	for _, a := range res.Refused {
		j.Refused = append(j.Refused, a.Reason)
	}
	// Every fix an agent could run, whether the finding refused the sync or left
	// something for a person.
	for _, a := range append(append([]plan.Action(nil), res.Done...), res.Actions...) {
		if a.Fix != "" && !contains(j.Fixes, a.Fix) {
			j.Fixes = append(j.Fixes, a.Fix)
		}
	}
	return j
}

// printNotes writes what sync found to stderr, one block per finding, each with its fix.
func printNotes(e Env, res *apply.Result) {
	for _, n := range res.Notes {
		fmt.Fprintf(e.Stderr, "outfit: %s\n", n)
	}
	for _, a := range res.Refused {
		fmt.Fprintf(e.Stderr, "outfit: not applied: %s\n", a.Reason)
		if a.Fix != "" {
			fmt.Fprintf(e.Stderr, "  fix: %s\n", a.Fix)
		}
	}
	if res.Quarantine != "" {
		fmt.Fprintf(e.Stderr, "outfit: copies saved in %s\n", res.Quarantine)
	}
}

// syncExit maps a result to the exit code: 3 for anything a person must look at.
func syncExit(res *apply.Result) int {
	if res.NeedsHuman || len(res.Refused) > 0 {
		return ExitNeedsHuman
	}
	return ExitOK
}

// cmdSync reconciles the worktree with the active profile. Silent on success;
// hooks and agents call it.
func cmdSync(ctx context.Context, e Env, args []string) int {
	fs := newFlags("sync")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "sync", pos, args); bad {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	if err := requireInstalled(l); err != nil {
		return fail(err)
	}
	res, _, err := converge(ctx, e, l, false)
	if err != nil {
		return fail(syncError(err))
	}
	if *asJSON {
		out, _ := json.Marshal(toJSON(res))
		fmt.Fprintln(e.Stdout, string(out))
		return syncExit(res)
	}
	printNotes(e, res)
	return syncExit(res)
}

// syncError makes a sync failure actionable: a busy index is retryable.
func syncError(err error) error {
	msg := err.Error()
	e := Errorf("%s", msg)
	switch {
	case strings.Contains(msg, "index.lock"):
		e.Retryable = true
		return e.WithFix("outfit sync   (another git command held the index; try again)")
	case strings.Contains(msg, "half done"):
		return e
	}
	return e.WithFix("outfit status")
}

// cmdUse switches the clone's (or with --here, this worktree's) profile and syncs.
func cmdUse(ctx context.Context, e Env, args []string) int {
	fs := newFlags("use")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	here := fs.Bool("here", false, "switch only this worktree, not the clone's default")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	if len(pos) != 1 {
		return fail(Usagef("usage: outfit use <profile> [--here]").WithFix("outfit ls"))
	}
	name := pos[0]
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	if err := requireInstalled(l); err != nil {
		return fail(err)
	}

	// The profile must exist somewhere; base always does.
	var chosen *profiles.Profile
	if name != config.Base {
		if err := profiles.ValidName(name); err != nil {
			return fail(Usagef("%v", err).WithFix("outfit ls"))
		}
		for _, p := range profiles.List(ctx, l.Git, l.Repo).Profiles {
			if p.Name == name {
				p := p
				chosen = &p
			}
		}
		if chosen == nil {
			return fail(Usagef("no profile called %q (committed, local or worktree)", name).WithFix("outfit ls"))
		}
	}

	// Settle the current profile first (absorb an editor's atomic save, say) so the
	// switch starts from a clean state.
	if pre, _, err := converge(ctx, e, l, false); err != nil {
		return fail(syncError(err))
	} else if pre.NeedsHuman || len(pre.Refused) > 0 {
		printNotes(e, pre)
		return syncExitJSON(e, *asJSON, pre, "settle the current profile first, then rerun `outfit use "+name+"`", "outfit sync")
	}

	// Never switch over uncommitted edits in the committed variant that is about to
	// go: git would leave those files in place and the tree half-switched. (After the
	// settle above, not before: absorbing an editor's save is what makes the variant
	// differ from git, and that edit must stop the switch too.)
	if l.Profile != nil && l.Profile.Tier == profiles.Committed && l.Profile.Name != name {
		if dirty := dirtyPaths(ctx, l, profiles.CommittedRoot+"/"+l.Profile.Name+"/"); len(dirty) > 0 {
			return fail((&Error{Code: ExitNeedsHuman, Msg: fmt.Sprintf("the active profile %q has uncommitted edits (%s): switching would leave the tree half-switched", l.Profile.Name, strings.Join(first(dirty, 3), ", "))}).
				WithFix("git add --sparse -u .outfit && git commit -m 'update profile'"))
		}
	}

	scope := "--local"
	scopeName := config.ScopeLocal
	if *here {
		if !l.Cfg.WorktreeConfig {
			return fail(Errorf("per-worktree settings are off, so --here has nowhere to go").WithFix("outfit init"))
		}
		scope, scopeName = "--worktree", config.ScopeWorktree
	}
	prev, hadPrev := l.Cfg.Value(scopeName, "profile")
	setProfile := func(v string, unset bool) error {
		if unset {
			_, err := l.Git.Write(ctx, "config", scope, "--unset-all", "outfit.profile")
			if err != nil && !strings.Contains(err.Error(), "exit 5") {
				return err
			}
			return nil
		}
		_, err := l.Git.Write(ctx, "config", scope, "outfit.profile", v)
		return err
	}
	// `use base` unsets the clone default; for a worktree it must say "base"
	// explicitly, or the clone's default would still win there.
	if name == config.Base && !*here {
		err = setProfile("", true)
	} else {
		err = setProfile(name, false)
	}
	if err != nil {
		return fail(partial(err, nil))
	}
	revert := func() {
		if hadPrev {
			_ = setProfile(prev, false)
		} else {
			_ = setProfile("", true)
		}
	}

	if l, err = load(ctx, e); err != nil {
		revert()
		return fail(err)
	}
	res, _, err := converge(ctx, e, l, true)
	if err != nil {
		revert()
		return fail(syncError(err))
	}
	if len(res.Refused) > 0 {
		revert()
	}
	shadow := ""
	if !*here {
		if v, ok := l.Cfg.Value(config.ScopeWorktree, "profile"); ok && v != name {
			shadow = fmt.Sprintf("this worktree still uses %q (set with --here): `outfit use %s --here` changes it", v, name)
		}
	}
	if *asJSON {
		j := toJSON(res)
		out, _ := json.Marshal(struct {
			Profile string `json:"profile"`
			Scope   string `json:"scope"`
			Shadow  string `json:"warning,omitempty"`
			syncJSON
		}{name, strings.TrimPrefix(scope, "--"), shadow, j})
		fmt.Fprintln(e.Stdout, string(out))
		return syncExit(res)
	}
	printNotes(e, res)
	if len(res.Refused) == 0 {
		where := "this clone"
		if *here {
			where = "this worktree"
		}
		files := 0
		for _, a := range res.Done {
			if a.Kind == plan.LinkPath || a.Kind == plan.Absorb {
				files++
			}
		}
		fmt.Fprintf(e.Stdout, "using %s for %s", name, where)
		if files > 0 {
			fmt.Fprintf(e.Stdout, " (%d file(s) swapped)", files)
		}
		fmt.Fprintln(e.Stdout)
	}
	if shadow != "" {
		fmt.Fprintf(e.Stderr, "warning: %s\n", shadow)
	}
	if w := untrackedProfileWarning(ctx, l); w != "" {
		fmt.Fprintf(e.Stderr, "warning: %s\n", w)
	}
	return syncExit(res)
}

// readProfileFile reads p below the profile's directory and can never leave it.
func readProfileFile(dir, p string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(p)
}

// untrackedProfileWarning names the committed-profile files git does not know yet
// (a profile made by hand, or just promoted): only the working tree holds them, so
// `git clean -xd` deletes them for good. Every profile directory is looked at, active or
// not. "" when there are none.
func untrackedProfileWarning(ctx context.Context, l *loaded) string {
	res, err := l.Git.Read(ctx, "--literal-pathspecs", "ls-files", "--others", "-z", "--", profiles.CommittedRoot)
	if err != nil {
		return ""
	}
	byProfile := map[string][]string{}
	var names []string
	for _, f := range strings.Split(string(res.Stdout), "\x00") {
		rest, ok := strings.CutPrefix(f, profiles.CommittedRoot+"/")
		if !ok || profiles.IsJunk(filepath.Base(f)) {
			continue
		}
		name, _, _ := strings.Cut(rest, "/")
		if profiles.ValidName(name) != nil {
			continue // leftovers (`.new-…`) and hand-made names are `ls`'s warnings, not something to commit
		}
		if byProfile[name] == nil {
			names = append(names, name)
		}
		byProfile[name] = append(byProfile[name], f)
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	var parts []string
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s (%s)", n, strings.Join(first(byProfile[n], 2), ", ")))
	}
	return fmt.Sprintf("profile files git does not have yet: %s. `git clean -xd` would delete them for good. Commit them: %s",
		strings.Join(parts, "; "), profiles.CommitCommand(names...))
}

// syncExitJSON prints a blocked result in the caller's format and returns exit 3.
func syncExitJSON(e Env, asJSON bool, res *apply.Result, reason, fix string) int {
	if asJSON {
		j := toJSON(res)
		j.Notes = append(j.Notes, reason)
		j.Fixes = append(j.Fixes, fix)
		out, _ := json.Marshal(j)
		fmt.Fprintln(e.Stdout, string(out))
	} else {
		fmt.Fprintf(e.Stderr, "outfit: %s\n  fix: %s\n", reason, fix)
	}
	return ExitNeedsHuman
}

// dirtyPaths lists paths under prefix with uncommitted changes (staged or
// unstaged; `.outfit/` is in info/exclude, so new files there are not listed), from
// one `git status`.
func dirtyPaths(ctx context.Context, l *loaded, prefix string) []string {
	res, err := l.Git.Read(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=no", "--", prefix)
	if err != nil {
		return nil
	}
	var out []string
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		if len(rec) > 3 {
			out = append(out, rec[3:])
		}
	}
	sort.Strings(out)
	return out
}

func first(s []string, n int) []string {
	if len(s) > n {
		return append(append([]string(nil), s[:n]...), fmt.Sprintf("…%d more", len(s)-n))
	}
	return s
}

// cmdShow prints an inactive (or active) profile's files without switching to it:
// committed ones straight from HEAD, local and worktree ones from disk.
func cmdShow(ctx context.Context, e Env, args []string) int {
	fs := newFlags("show")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	if len(pos) < 1 || len(pos) > 2 {
		return fail(Usagef("usage: outfit show <profile> [path]").WithFix("outfit ls"))
	}
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	var prof *profiles.Profile
	if p, rerr := profiles.Resolve(profiles.List(ctx, l.Git, l.Repo), pos[0]); rerr == nil {
		prof = &p
	}
	if prof == nil {
		return fail(Usagef("no profile called %q", pos[0]).WithFix("outfit ls"))
	}
	var files []string
	var cfg *config.File
	if prof.Tier == profiles.Committed {
		files = profiles.HeadFiles(ctx, l.Git, prof.Name)
		cfg = profiles.HeadConfig(ctx, l.Git, prof.Name)
	} else {
		files = profiles.Files(*prof)
		cfg, _ = profiles.ReadConfig(*prof)
	}
	if len(pos) == 2 {
		p := filepath.ToSlash(pos[1])
		if !contains(files, p) {
			return fail(Usagef("profile %q has no file %q (it has: %s)", prof.Name, p, strings.Join(files, ", ")).WithFix("outfit show " + prof.Name))
		}
		var b []byte
		if prof.Tier == profiles.Committed {
			res, err := l.Git.Read(ctx, "show", "HEAD:"+profiles.CommittedRoot+"/"+prof.Name+"/"+p)
			if err != nil {
				return fail(Errorf("cannot read %s at HEAD: %v", p, err))
			}
			b = res.Stdout
		} else if b, err = readProfileFile(prof.Dir, p); err != nil {
			return fail(Errorf("cannot read %s: %v", p, err))
		}
		_, _ = e.Stdout.Write(b)
		return ExitOK
	}
	var keys []string
	if cfg != nil {
		for k, v := range cfg.Keys {
			keys = append(keys, k+"="+show(v))
		}
		sort.Strings(keys)
	}
	if *asJSON {
		out, _ := json.Marshal(struct {
			Name    string   `json:"name"`
			Tier    string   `json:"tier"`
			Toggles []string `json:"toggles"`
			Files   []string `json:"files"`
			Dir     string   `json:"dir,omitempty"`
		}{prof.Name, string(prof.Tier), nonNilStrings(keys), nonNilStrings(files), personalDir(*prof)})
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}
	fmt.Fprintf(e.Stdout, "profile  %s (%s)\n", esc(prof.Name), prof.Tier)
	if d := personalDir(*prof); d != "" {
		fmt.Fprintf(e.Stdout, "dir      %s\n", d)
	}
	if len(keys) == 0 {
		fmt.Fprintln(e.Stdout, "toggles  (none)")
	} else {
		fmt.Fprintf(e.Stdout, "toggles  %s\n", strings.Join(keys, " "))
	}
	if len(files) == 0 {
		fmt.Fprintln(e.Stdout, "files    (none)")
	}
	for i, f := range files {
		label := "         "
		if i == 0 {
			label = "files    "
		}
		fmt.Fprintf(e.Stdout, "%s%s\n", label, esc(f))
	}
	return ExitOK
}

// personalDir is where a local or worktree profile's files are ("" for a committed one,
// which is in the repository).
func personalDir(p profiles.Profile) string {
	if p.Tier == profiles.Committed {
		return ""
	}
	return p.Dir
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// cmdAuthor turns author mode on (every committed profile on disk, to edit side
// by side) or off (only the active one).
func cmdAuthor(ctx context.Context, e Env, args []string) int {
	fs := newFlags("author")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	if len(pos) != 1 || (pos[0] != "on" && pos[0] != "off") {
		return fail(Usagef("usage: outfit author on|off").WithFix("outfit author on"))
	}
	on := pos[0] == "on"
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	if err := requireInstalled(l); err != nil {
		return fail(err)
	}
	st, serr := state.Read(l.Repo.GitDir)
	if errors.Is(serr, state.ErrNone) {
		st, serr = &state.State{}, nil
	}
	if serr != nil {
		return fail(Errorf("%v", serr).WithFix("outfit init"))
	}
	if !on {
		active := ""
		if l.Profile != nil && l.Profile.Tier == profiles.Committed {
			active = l.Profile.Name
		}
		var dirty []string
		for _, p := range dirtyPaths(ctx, l, profiles.CommittedRoot+"/") {
			if active == "" || !strings.HasPrefix(p, profiles.CommittedRoot+"/"+active+"/") {
				dirty = append(dirty, p)
			}
		}
		if len(dirty) > 0 {
			return fail((&Error{Code: ExitNeedsHuman, Msg: fmt.Sprintf("profiles other than the active one have uncommitted edits (%s): hiding them would leave the tree half-switched", strings.Join(first(dirty, 3), ", "))}).
				WithFix("git add --sparse -u .outfit && git commit -m 'update profiles'"))
		}
	}
	prev := st.Author
	st.Author = on
	if _, err := state.Write(l.Repo.GitDir, st); err != nil {
		return fail(Errorf("cannot record author mode: %v", err))
	}
	res, _, err := converge(ctx, e, l, true)
	if err != nil || len(res.Refused) > 0 {
		st.Author = prev
		_, _ = state.Write(l.Repo.GitDir, st)
		if err != nil {
			return fail(syncError(err))
		}
	}
	if *asJSON {
		out, _ := json.Marshal(struct {
			Author bool `json:"author"`
			syncJSON
		}{on, toJSON(res)})
		fmt.Fprintln(e.Stdout, string(out))
		return syncExit(res)
	}
	printNotes(e, res)
	if len(res.Refused) == 0 {
		if on {
			fmt.Fprintln(e.Stdout, "author mode on: every committed profile is on disk under .outfit/profiles/ (outfit author off hides them again)")
		} else {
			fmt.Fprintln(e.Stdout, "author mode off: only the active profile is on disk")
		}
	}
	return syncExit(res)
}
