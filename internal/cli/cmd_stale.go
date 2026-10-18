package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/apply"
	"github.com/prosperity-solutions/outfit/internal/plan"
)

// swappedPath turns the argument of `diff`/`ack` (relative to where the person is,
// or absolute) into the path relative to the worktree that state.json records.
func swappedPath(e Env, l *loaded, arg string) (string, error) {
	p := arg
	if !filepath.IsAbs(p) {
		p = filepath.Join(e.Dir, p)
	}
	rel, err := filepath.Rel(l.Repo.WorkTree, filepath.Clean(p))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", Usagef("%s is not inside this worktree", arg).WithFix("outfit status")
	}
	return filepath.ToSlash(rel), nil
}

// staleCommand is the common start of `diff` and `ack`.
func staleCommand(ctx context.Context, e Env, name string, args []string) (l *loaded, in apply.Input, path string, asJSON bool, code int, done bool) {
	fs := newFlags(name)
	j := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return nil, in, "", false, code, true
	}
	fail := func(err error) (*loaded, apply.Input, string, bool, int, bool) {
		return nil, in, "", *j, report(e.Stdout, e.Stderr, *j, err), true
	}
	if len(pos) != 1 {
		return fail(Usagef("usage: outfit %s <path>", name).WithFix("outfit status"))
	}
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	if requireInstalled(l) != nil {
		return fail(Errorf("nothing is swapped in this repository yet (outfit is not installed here), so there is nothing to compare or acknowledge").WithFix("outfit use <profile>"))
	}
	p, err := swappedPath(e, l, pos[0])
	if err != nil {
		return fail(err)
	}
	in = syncInput(ctx, e, l, false)
	// The fixes outfit prints name paths from the worktree root; from a subdirectory
	// that is not where "AGENTS.md" is relative to. Try the root-relative reading too.
	if _, berr := apply.BaseOf(ctx, in, p); errors.Is(berr, apply.ErrNotSwapped) && !filepath.IsAbs(pos[0]) {
		if alt := filepath.ToSlash(filepath.Clean(pos[0])); alt != p && !strings.HasPrefix(alt, "../") {
			if _, aerr := apply.BaseOf(ctx, in, alt); aerr == nil {
				p = alt
			}
		}
	}
	return l, in, p, *j, 0, false
}

// isObjectID reports a full-length hex object name (SHA-1 or SHA-256).
func isObjectID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func notSwapped(path string) error {
	return Errorf("%s is not swapped by outfit, so there is no base to compare", path).WithFix("outfit status")
}

// cmdDiff shows what changed in a swapped path's base file since outfit
// swapped it: the profile's copy stays as it is, this is what it no longer sits on.
func cmdDiff(ctx context.Context, e Env, args []string) int {
	l, in, path, asJSON, code, done := staleCommand(ctx, e, "diff", args)
	if done {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, asJSON, err) }
	if apply.IsReplacedDir(in, path) {
		note := path + " is a directory your profile replaces: the team's files below it are hidden and yours stand in. outfit remembers a digest of the team's files, not a commit, so there is no exact diff: what the team changed there is in `git log --stat -- " + plan.Quote(path) + "` (the recent commits). When that is fine: outfit ack " + plan.Quote(path)
		if asJSON {
			out, _ := json.Marshal(struct {
				Path     string `json:"path"`
				Replaced bool   `json:"replaced"`
				Note     string `json:"note"`
			}{path, true, note})
			fmt.Fprintln(e.Stdout, string(out))
			return ExitOK
		}
		fmt.Fprintln(e.Stdout, note)
		return ExitOK
	}
	b, err := apply.BaseOf(ctx, in, path)
	if errors.Is(err, apply.ErrNotSwapped) {
		return fail(notSwapped(path))
	}
	if err != nil {
		return fail(Errorf("%v", err).WithFix("outfit status"))
	}
	// What goes to git as an object name must be one: state.json is a file someone
	// else may have written.
	if !isObjectID(b.Current) || (b.Recorded != "" && !isObjectID(b.Recorded)) {
		return fail(Errorf("the recorded base of %s is not a git object id: state.json is damaged", path).WithFix("outfit init"))
	}
	stale := b.Recorded != "" && !b.Known
	var text string
	if stale {
		res, err := l.Git.Read(ctx, "diff", "--no-ext-diff", "--no-color", b.Recorded, b.Current)
		if err != nil {
			// The recorded version may be gone (a deleted branch, a gc): then there is
			// nothing to compare, but the current base can still be read and acknowledged.
			return fail(Errorf("cannot compare the two versions of %s (the one outfit swapped over is probably no longer in this repository): %v", path, err).
				WithFix("git show :" + plan.Quote(path) + "   (the base as it is now; when the profile is fine with it: outfit ack " + plan.Quote(path) + ")"))
		}
		text = strings.ReplaceAll(strings.ReplaceAll(string(res.Stdout), "a/"+b.Recorded, "a/"+path+" (swapped over)"), "b/"+b.Current, "b/"+path+" (now)")
	}
	if asJSON {
		out, _ := json.Marshal(struct {
			Path     string `json:"path"`
			Stale    bool   `json:"stale"`
			Recorded string `json:"recorded,omitempty"`
			Current  string `json:"current,omitempty"`
			Diff     string `json:"diff"`
		}{path, stale, b.Recorded, b.Current, text})
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}
	if !stale {
		fmt.Fprintf(e.Stdout, "%s: the base file is as it was when outfit swapped it (or you acknowledged it); nothing to look at\n", path)
		return ExitOK
	}
	fmt.Fprintf(e.Stdout, "# %s: the base file is different from when outfit swapped it (was %.8s, is %.8s); the profile's copy is kept as it is\n", path, b.Recorded, b.Current)
	fmt.Fprint(e.Stdout, text)
	fmt.Fprintf(e.Stdout, "# the whole new base: git show :%s\n# when the profile is fine as it is: outfit ack %s\n", plan.Quote(path), plan.Quote(path))
	return ExitOK
}

// cmdAck records the base file's current version as the one the swap stands on.
func cmdAck(ctx context.Context, e Env, args []string) int {
	_, in, path, asJSON, code, done := staleCommand(ctx, e, "ack", args)
	if done {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, asJSON, err) }
	var changed bool
	var err error
	if apply.IsReplacedDir(in, path) {
		changed, err = apply.AckDir(ctx, in, path)
	} else {
		changed, err = apply.Ack(ctx, in, path)
	}
	if errors.Is(err, apply.ErrNotSwapped) {
		return fail(notSwapped(path))
	}
	if err != nil {
		return fail(Errorf("%v", err).WithFix("outfit status"))
	}
	if asJSON {
		out, _ := json.Marshal(struct {
			Path    string `json:"path"`
			Changed bool   `json:"changed"`
		}{path, changed})
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}
	if changed {
		fmt.Fprintf(e.Stdout, "%s: the new base is recorded; the profile's copy is kept\n", path)
	} else {
		fmt.Fprintf(e.Stdout, "%s: nothing to acknowledge, the base is as recorded\n", path)
	}
	return ExitOK
}
