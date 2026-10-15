package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/apply"
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
	if err := requireInstalled(l); err != nil {
		return fail(err)
	}
	p, err := swappedPath(e, l, pos[0])
	if err != nil {
		return fail(err)
	}
	return l, syncInput(ctx, e, l, false), p, *j, 0, false
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

// cmdDiff shows what changed in a swapped path's base file upstream since outfit
// swapped it: the profile's copy stays as it is, this is what it no longer sits on.
func cmdDiff(ctx context.Context, e Env, args []string) int {
	l, in, path, asJSON, code, done := staleCommand(ctx, e, "diff", args)
	if done {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, asJSON, err) }
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
	stale := b.Recorded != "" && b.Recorded != b.Current
	var text string
	if stale {
		res, err := l.Git.Read(ctx, "diff", "--no-ext-diff", "--no-color", b.Recorded, b.Current)
		if err != nil {
			return fail(Errorf("cannot compare the two versions of %s: %v", path, err).WithFix("outfit status"))
		}
		text = string(res.Stdout)
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
		fmt.Fprintf(e.Stdout, "%s: the base file is as it was when outfit swapped it; nothing to look at\n", path)
		return ExitOK
	}
	fmt.Fprintf(e.Stdout, "# %s: the base file changed upstream (was %.8s, is %.8s); the profile's copy is kept as it is\n", path, b.Recorded, b.Current)
	fmt.Fprint(e.Stdout, text)
	fmt.Fprintf(e.Stdout, "# when the profile is fine as it is: outfit ack %s\n", path)
	return ExitOK
}

// cmdAck records the base file's current version as the one the swap stands on.
func cmdAck(ctx context.Context, e Env, args []string) int {
	_, in, path, asJSON, code, done := staleCommand(ctx, e, "ack", args)
	if done {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, asJSON, err) }
	changed, err := apply.Ack(ctx, in, path)
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
