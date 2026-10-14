package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
)

type setChange struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Unset bool   `json:"unset"`
	Scope string `json:"scope"`
	// Changed is false when the request was already satisfied (idempotent): the
	// value was already set, or an unset found nothing to remove.
	Changed bool `json:"changed"`
	// StillSetIn lists the other sources of the key after an unset.
	StillSetIn []string `json:"still_set_in"`
}

// cmdSet writes personal overrides into git config: --local by default (this
// clone), --here for this worktree, --global for this machine. `key=` unsets.
func cmdSet(ctx context.Context, e Env, args []string) int {
	fs := newFlags("set")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	here := fs.Bool("here", false, "set it for this worktree only")
	global := fs.Bool("global", false, "set it for every repo on this machine")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }
	if len(pos) == 0 || (*here && *global) {
		return fail(Usagef("usage: outfit set <key>=<value>… [--here | --global]").WithFix("outfit set biome=off --global"))
	}

	type kv struct {
		key, val string
		unset    bool
	}
	var todo []kv
	for _, a := range pos {
		k, v, ok := strings.Cut(a, "=")
		if !ok {
			return fail(Usagef("%q has no `=`: write key=value, or key= to unset", a).WithFix("outfit set " + a + "=off"))
		}
		nk, err := config.NormalizeKey(k)
		if err != nil {
			return fail(Usagef("%v", err))
		}
		if config.IsReserved(nk) {
			fix := "git config --local outfit.profile <name>"
			if nk == config.KeyLocked {
				fix = "edit .outfit/config in a PR"
			}
			return fail(Usagef("%q is not a personal toggle", nk).WithFix(fix))
		}
		todo = append(todo, kv{nk, normalizeBool(v), v == ""})
	}

	l, err := load(ctx, e)
	if err == errNotInRepo && *global {
		// --global is machine-wide: it needs no repository (and so no lock check).
		l, err = &loaded{Git: gitx.Client{Dir: e.Dir}}, nil
	}
	if err != nil {
		return fail(err)
	}
	scope, flagName := config.ScopeLocal, "--local"
	switch {
	case *global:
		scope, flagName = config.ScopeGlobal, "--global"
	case *here:
		scope, flagName = config.ScopeWorktree, "--worktree"
		if !l.Cfg.WorktreeConfig {
			return fail(Errorf("this repository has no per-worktree config yet (extensions.worktreeConfig is off)").WithFix("outfit init"))
		}
	}
	for _, t := range todo {
		if !t.unset && l.Result.IsLocked(t.key) {
			return fail(Errorf("%q is locked by .outfit/config: a personal override would be ignored", t.key).
				WithFix("ask the team to unlock it in .outfit/config"))
		}
	}

	changes := []setChange{}
	for _, t := range todo {
		name := "outfit." + t.key
		ch := setChange{Key: t.key, Value: t.val, Unset: t.unset, Scope: scope, StillSetIn: []string{}}
		if t.unset {
			// Exit 5 means "nothing to unset": already as requested. The exit code,
			// not our snapshot, decides Changed, so it is right even without a repo.
			res, err := l.Git.Write(ctx, "config", flagName, "--unset-all", name)
			if err != nil && res.ExitCode != 5 {
				return fail(partial(err, changes))
			}
			ch.Changed = err == nil
			// Unsetting one scope does not unset the others: say who still sets it.
			ch.StillSetIn = l.Cfg.OtherScopes(scope, t.key)
		} else {
			// --replace-all converges even when the key holds several values.
			if _, err := l.Git.Write(ctx, "config", flagName, "--replace-all", name, t.val); err != nil {
				return fail(partial(err, changes))
			}
			prev, had := l.Cfg.Value(scope, t.key)
			ch.Changed = !had || prev != t.val
		}
		changes = append(changes, ch)
	}

	if *asJSON {
		// An object, not a bare array, so fields can be added without breaking
		// consumers.
		out, _ := json.Marshal(struct {
			Changes []setChange `json:"changes"`
		}{changes})
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}
	for _, c := range changes {
		switch {
		case c.Unset && !c.Changed:
			fmt.Fprintf(e.Stdout, "%s was not set (%s)", c.Key, c.Scope)
		case c.Unset:
			fmt.Fprintf(e.Stdout, "%s unset (%s)", c.Key, c.Scope)
		default:
			fmt.Fprintf(e.Stdout, "%s=%s (%s)", c.Key, show(c.Value), c.Scope)
		}
		if len(c.StillSetIn) > 0 {
			fmt.Fprintf(e.Stdout, "; still set in: %s", strings.Join(c.StillSetIn, ", "))
		}
		fmt.Fprintln(e.Stdout)
	}
	return ExitOK
}

// partial turns a git failure into an error that says what was already applied
// (set is not atomic across keys) and marks lock contention retryable.
func partial(err error, applied []setChange) *Error {
	msg := err.Error()
	if len(applied) > 0 {
		var done []string
		for _, c := range applied {
			done = append(done, c.Key)
		}
		msg += " (already applied: " + strings.Join(done, ", ") + ")"
	}
	e := Errorf("%s", msg)
	if strings.Contains(msg, "could not lock config file") {
		e.Retryable = true
		e.Fix = "rerun the same command"
	}
	return e
}

// normalizeBool maps git's boolean spellings to the two strings hooks compare
// against. Anything else is a free-form value and stays as typed.
func normalizeBool(v string) string {
	switch strings.ToLower(v) {
	case "on", "true", "yes", "1":
		return "on"
	case "off", "false", "no", "0":
		return "off"
	}
	return v
}
