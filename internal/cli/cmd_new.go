package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/profiles"
)

// cmdNew creates a personal profile: local (this clone, never versioned) or, with
// --here, a worktree profile that goes away with the worktree. No history, no
// snapshots: plain files, edited in place.
func cmdNew(ctx context.Context, e Env, args []string) int {
	fs := newFlags("new")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	from := fs.String("from", "", "start from this profile (name, or tier:name for a shadowed one) instead of from nothing")
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
	if src == nil {
		fmt.Fprintf(e.Stdout, "  put files in it at the same relative paths as the tracked files they replace (e.g. %s), and toggles in profile.config\n", filepath.Join(c.Dir, "AGENTS.md"))
	}
	fmt.Fprintf(e.Stdout, "  then: %s\n", use)
	return ExitOK
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
	case p.Tier == profiles.Committed:
		return fail(Usagef("%q is already a committed profile", name).WithFix("outfit ls"))
	case p.Tier == profiles.Worktree:
		return fail(Usagef("%q is a worktree profile: only local profiles are promoted", name).WithFix("outfit new <name> --from worktree:" + name + "   (a local copy), then: outfit promote <name>"))
	}
	wasActive := l.Result.Profile.Name == name
	c, err := profiles.Promote(ctx, l.Git, l.Repo, listing, name)
	if err != nil {
		return fail(Errorf("%v", err).WithFix("outfit ls"))
	}
	rel := profiles.CommittedRoot + "/" + name
	next := "git add --sparse -f " + plan.Quote(rel) + " && git commit -m " + plan.Quote("feat: add profile "+name)

	// Where the profile is in use its links pointed into the local copy that is gone:
	// settle them now (other worktrees follow on their next sync).
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
			Name  string    `json:"name"`
			Dir   string    `json:"dir"`
			Files int       `json:"files"`
			Next  string    `json:"next"`
			Sync  *syncJSON `json:"sync,omitempty"`
			Error string    `json:"sync_error,omitempty"`
		}{name, c.Dir, c.Files, next, res, errString(syncErr)})
		fmt.Fprintln(e.Stdout, string(out))
		if syncErr != nil {
			return ExitNeedsHuman
		}
		return ExitOK
	}
	fmt.Fprintf(e.Stdout, "promoted %s to %s (%d file(s)); the local copy is gone\n", name, rel, c.Files)
	fmt.Fprintf(e.Stdout, "  git does not have it yet: commit it so teammates get it (until then `git clean -x` deletes it):\n  %s\n", next)
	if syncErr != nil {
		fmt.Fprintf(e.Stderr, "outfit: the files could not be re-linked: %v\n  fix: outfit sync\n", syncErr)
		return ExitNeedsHuman
	}
	return ExitOK
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
