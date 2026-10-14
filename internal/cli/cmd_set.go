package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/config"
)

type setChange struct {
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
	Unset bool   `json:"unset,omitempty"`
	Scope string `json:"scope"`
}

// cmdSet writes personal overrides into git config: --local by default (this
// clone), --here for this worktree, --global for this machine. `key=` unsets.
func cmdSet(ctx context.Context, e Env, args []string) int {
	fs := flag.NewFlagSet("set", flag.ContinueOnError)
	fs.SetOutput(e.Stderr)
	asJSON := fs.Bool("json", false, "print machine-readable output")
	here := fs.Bool("here", false, "set it for this worktree only")
	global := fs.Bool("global", false, "set it for every repo on this machine")
	pos, err := parse(fs, args)
	if err != nil {
		return ExitUsage
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
			fix := "outfit use <profile>"
			if nk == config.KeyLocked {
				fix = "edit .outfit/config in a PR"
			}
			return fail(Usagef("%q is not a personal toggle", nk).WithFix(fix))
		}
		todo = append(todo, kv{nk, v, v == ""})
	}

	l, err := load(ctx, e)
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

	var changes []setChange
	for _, t := range todo {
		name := "outfit." + t.key
		if t.unset {
			// Exit 5 means "nothing to unset": already as requested.
			if res, err := l.Git.Write(ctx, "config", flagName, "--unset-all", name); err != nil && res.ExitCode != 5 {
				return fail(Errorf("%v", err))
			}
		} else if _, err := l.Git.Write(ctx, "config", flagName, name, t.val); err != nil {
			return fail(Errorf("%v", err))
		}
		changes = append(changes, setChange{Key: t.key, Value: t.val, Unset: t.unset, Scope: scope})
	}

	if *asJSON {
		out, _ := json.Marshal(changes)
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}
	for _, c := range changes {
		if c.Unset {
			fmt.Fprintf(e.Stdout, "%s unset (%s)\n", c.Key, c.Scope)
		} else {
			fmt.Fprintf(e.Stdout, "%s=%s (%s)\n", c.Key, c.Value, c.Scope)
		}
	}
	return ExitOK
}
