package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/hooks"
	"github.com/prosperity-solutions/outfit/internal/profiles"
)

// cmdNew creates a personal profile: local (this clone, never versioned) or, with
// --here, a worktree profile that goes away with the worktree. No history, no
// snapshots: plain files, edited in place.
func cmdNew(ctx context.Context, e Env, args []string) int {
	fs := newFlags("new")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	from := fs.String("from", "", "start from this profile (name, or tier:name for a shadowed one; base or nothing: empty)")
	here := fs.Bool("here", false, "make a worktree profile (deleted with this worktree) instead of a local one")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	if len(pos) != 1 {
		return fail(Usagef("usage: outfit new <name> [--from <profile>] [--here]").WithFix("outfit ls"))
	}
	name := pos[0]
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	listing := profiles.List(ctx, l.Git, l.Repo)

	var src *profiles.Profile
	if *from != "" && *from != "base" {
		p, rerr := profiles.Resolve(listing, *from)
		if rerr != nil {
			return fail(Usagef("%v", rerr).WithFix("outfit ls"))
		}
		src = &p
	}
	tier := profiles.Local
	if *here {
		tier = profiles.Worktree
	}
	// A worktree profile of ANOTHER worktree is invisible to this listing: a local
	// profile of the same name would silently replace it there on its next sync.
	if prev := worktreeProfileOwner(l, name); prev != "" && profiles.ValidName(name) == nil {
		return fail(Errorf("a worktree profile called %q already exists in the worktree %s (names are unique across committed, local and worktree profiles)", name, prev).WithFix("outfit new <another-name>" + fromHint(*from)))
	}
	c, err := profiles.Create(ctx, l.Git, l.Repo, listing, name, tier, src)
	if err != nil {
		if verr := profiles.ValidName(name); verr != nil {
			return fail(Usagef("%v", err).WithFix("outfit new <another-name>"))
		}
		if _, taken := profiles.Exists(listing, name); taken {
			return fail(Errorf("%v", err).WithFix("outfit new <another-name>" + fromHint(*from)))
		}
		return fail(Errorf("cannot create the profile: %v", err))
	}
	use := "outfit use " + name
	if *here {
		use += " --here"
	}
	origin := "nothing (it swaps no files yet)"
	if src != nil {
		origin = fmt.Sprintf("%s (%d file(s) copied)", src.Name, c.Files)
	}
	if *asJSON {
		out, _ := json.Marshal(struct {
			Name  string `json:"name"`
			Tier  string `json:"tier"`
			Dir   string `json:"dir"`
			From  string `json:"from,omitempty"`
			Files int    `json:"files"`
			Next  string `json:"next"`
		}{name, string(tier), c.Dir, *from, c.Files, use})
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}
	fmt.Fprintf(e.Stdout, "created %s profile %s from %s\n  %s\n", tier, name, origin, c.Dir)
	if len(c.Skipped) > 0 {
		fmt.Fprintf(e.Stderr, "note: %d link(s) or special file(s) were not copied (%s): profiles hold plain files only\n", len(c.Skipped), strings.Join(c.Skipped, ", "))
	}
	if src == nil {
		fmt.Fprintf(e.Stdout, "  put files in it at the same relative paths as the tracked files they replace (e.g. %s), and toggles in profile.config\n", filepath.Join(c.Dir, "AGENTS.md"))
	}
	fmt.Fprintf(e.Stdout, "  then: %s\n", use)
	return ExitOK
}

// worktreeProfileOwner is the path of the worktree whose worktree profiles include
// name ("" when none does).
func worktreeProfileOwner(l *loaded, name string) string {
	for _, w := range hooks.Worktrees(l.Repo.CommonDir) {
		// A worktree whose directory is gone (prunable) has dead profiles: they do not
		// hold a name.
		if w.Path != "" && !profiles.RealDir(w.Path) {
			continue
		}
		if profiles.RealDir(filepath.Join(profiles.TierDirIn(w.Path, l.Repo.CommonDir, w.GitDir, profiles.Worktree), name)) {
			if w.Path != "" {
				return w.Path
			}
			return w.GitDir
		}
	}
	return ""
}

func fromHint(from string) string {
	if from != "" {
		return " --from " + from
	}
	return ""
}

// cmdPromote copies a local profile into .outfit/profiles/<name>/ so git can version
// it, and removes the local copy.
func cmdPromote(ctx context.Context, e Env, args []string) int {
	fs := newFlags("promote")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	if len(pos) != 1 {
		return fail(Usagef("usage: outfit promote <name>").WithFix("outfit ls"))
	}
	name := pos[0]
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	listing := profiles.List(ctx, l.Git, l.Repo)
	p, rerr := profiles.Resolve(listing, name)
	switch {
	case rerr != nil:
		return fail(Usagef("%v", rerr).WithFix("outfit ls"))
	case p.Tier == profiles.Committed && len(profiles.HeadFiles(ctx, l.Git, name)) == 0:
		return fail(Usagef("%s/%s exists in this worktree but git does not have it: it is not a local profile, so there is nothing to promote", profiles.CommittedRoot, name).
			WithFix(profiles.CommitCommand(name)))
	case p.Tier == profiles.Committed:
		return fail(Usagef("%q is already a committed profile", name).WithFix("outfit ls"))
	case p.Tier == profiles.Worktree:
		return fail(Usagef("%q is a worktree profile: only local profiles are promoted (make a local copy under another name, then promote that)", name).WithFix("outfit new <other-name> --from worktree:" + name))
	}
	wasActive := l.Result.Profile.Name == name
	others := worktreesUsing(ctx, l, name) // looked at BEFORE the local copy goes
	c, err := profiles.Promote(ctx, l.Git, l.Repo, listing, name)
	if err != nil {
		return fail(Errorf("%v", err).WithFix("outfit ls"))
	}
	rel := profiles.CommittedRoot + "/" + name
	next := profiles.CommitCommand(name)

	// Where the profile is in use its links pointed into the local copy that is gone:
	// settle them now, here and in the other worktrees that use it (before any output, so
	// --json and the text agree).
	var settled, unsettled []string
	for _, w := range others {
		if resyncWorktree(ctx, e, w) {
			settled = append(settled, w.label())
		} else {
			unsettled = append(unsettled, w.label())
		}
	}
	var res *syncJSON
	var syncErr error
	if wasActive && requireInstalled(l) == nil {
		if l2, lerr := load(ctx, e); lerr != nil {
			syncErr = lerr
		} else if r, _, serr := converge(ctx, e, l2, false); serr != nil {
			syncErr = serr
		} else {
			j := toJSON(r)
			res = &j
			if !*asJSON {
				printNotes(e, r)
			}
		}
	}
	if *asJSON {
		out, _ := json.Marshal(struct {
			Name   string    `json:"name"`
			Dir    string    `json:"dir"`
			Files  int       `json:"files"`
			Next   string    `json:"next"`
			Others []string  `json:"other_worktrees_using_it"`
			Sync   *syncJSON `json:"sync,omitempty"`
			Error  string    `json:"sync_error,omitempty"`
		}{name, c.Dir, c.Files, next, worktreeLabels(others), res, errString(syncErr)})
		fmt.Fprintln(e.Stdout, string(out))
		if syncErr != nil {
			return ExitNeedsHuman
		}
		return ExitOK
	}
	fmt.Fprintf(e.Stdout, "promoted %s to %s (%d file(s)); the local copy is gone\n", name, rel, c.Files)
	fmt.Fprintf(e.Stdout, "  git does not have it yet: commit it so teammates get it (until then `git clean -xd` deletes it):\n  %s\n", next)
	if len(settled) > 0 {
		fmt.Fprintf(e.Stderr, "warning: other worktrees use %q (%s): they are on base now (the committed copy exists only here until it is committed and they have the commit)\n", name, strings.Join(settled, ", "))
	}
	if len(unsettled) > 0 {
		fmt.Fprintf(e.Stderr, "warning: other worktrees use %q and could not be re-synced from here (%s): their links point into the removed local copy until `outfit sync` runs there\n", name, strings.Join(unsettled, ", "))
	}
	if syncErr != nil {
		fmt.Fprintf(e.Stderr, "outfit: the files could not be re-linked: %v\n  fix: outfit sync\n", syncErr)
		return ExitNeedsHuman
	}
	return ExitOK
}

// worktreesUsing lists the OTHER worktrees whose effective profile is name (their own
// pin, else the clone's default): promote removes the only copy they can see.
func worktreesUsing(ctx context.Context, l *loaded, name string) []uninstallWorktree {
	var out []uninstallWorktree
	for _, w := range uninstallWorktrees(l) {
		if w.GitDir == l.Repo.GitDir {
			continue
		}
		g := gitx.Client{Dir: w.Path, OwnRepo: true}
		if w.Path == "" {
			g = gitx.Client{OwnRepo: true, Env: []string{"GIT_DIR=" + w.GitDir}} // the main worktree of a separate git dir, path unknown
		}
		if res, err := g.Read(ctx, "config", "--get", "outfit.profile"); err == nil && strings.TrimSpace(string(res.Stdout)) == name {
			out = append(out, w)
		}
	}
	return out
}

// resyncWorktree runs sync for another worktree in this process (no subprocess): the
// same load-and-converge `outfit sync` does there. It reports whether that worked.
func resyncWorktree(ctx context.Context, e Env, w uninstallWorktree) bool {
	if w.Path == "" {
		return false
	}
	other := e
	other.Dir = w.Path
	other.OwnRepo = true
	other.Getenv = func(k string) string {
		if k == "GIT_DIR" || k == "GIT_WORK_TREE" || k == "GIT_INDEX_FILE" {
			return "" // the repository is found from the directory, not from this process's
		}
		return e.Getenv(k)
	}
	l, err := load(ctx, other)
	if err != nil || requireInstalled(l) != nil {
		return false
	}
	res, _, err := converge(ctx, other, l, false)
	// Settled only when sync is done: a refusal or a "needs a person" is not.
	return err == nil && len(res.Refused) == 0 && !res.NeedsHuman
}

func worktreeLabels(ws []uninstallWorktree) []string {
	out := []string{}
	for _, w := range ws {
		out = append(out, w.label())
	}
	return out
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
