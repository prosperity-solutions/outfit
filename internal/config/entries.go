package config

import (
	"bytes"
	"fmt"
	"strings"
)

// Scopes as printed by `git config --list --show-scope`.
const (
	ScopeSystem   = "system"
	ScopeGlobal   = "global"
	ScopeLocal    = "local"
	ScopeWorktree = "worktree"
	ScopeCommand  = "command"
)

// Entry is one `outfit.*` line from git config.
type Entry struct {
	Scope string
	// Key is the normalized key without the `outfit.` prefix ("biome",
	// "hook.pre-commit", "profile").
	Key   string
	Value string
}

// GitConfig is what one `git config --list --show-scope -z` call yields that
// outfit cares about.
type GitConfig struct {
	// Entries are the outfit.* settings in git's own order, so a later entry of
	// the same scope overrides an earlier one.
	Entries []Entry
	// WorktreeConfig reports extensions.worktreeConfig=true IN THE REPO'S OWN
	// config: git ignores the extension anywhere else.
	WorktreeConfig bool
	// SawLocal reports that git printed at least one `local`-scope entry. A repo
	// always has some (core.repositoryformatversion): none means git refused to
	// read this repository's config, e.g. safe.directory "dubious ownership".
	SawLocal bool
	// Warnings lists outfit.* keys that were not valid.
	Warnings []Warning
}

// ParseGitList parses the output of `git config --list --show-scope -z`:
//
//	<scope> NUL <key> LF <value> NUL      (a bare key has no LF/value)
//
// Only outfit.* and extensions.worktreeconfig are kept.
func ParseGitList(out []byte) GitConfig {
	var gc GitConfig
	rest := out
	for len(rest) > 0 {
		scope, r, ok := bytes.Cut(rest, []byte{0})
		if !ok {
			break
		}
		rec, r2, _ := bytes.Cut(r, []byte{0})
		rest = r2
		key, val, hasVal := strings.Cut(string(rec), "\n")
		if !hasVal {
			val = "true"
		}
		if string(scope) == ScopeLocal {
			gc.SawLocal = true
		}
		lk := strings.ToLower(key)
		if lk == "extensions.worktreeconfig" {
			if string(scope) == ScopeLocal {
				gc.WorktreeConfig = gitBool(val)
			}
			continue
		}
		name, ok := strings.CutPrefix(key, "outfit.")
		if !ok {
			// git lowercases section names, so "Outfit.x" arrives as "outfit.x".
			continue
		}
		nk, err := NormalizeKey(name)
		// git lowercases section and variable names but keeps the subsection's
		// case, so an upper-case letter here means `outfit.Hook.x`, a different
		// key from `outfit.hook.x` that `outfit set` could never unset.
		if err == nil && nk != name {
			err = fmt.Errorf("keys must be lowercase")
		}
		if err != nil {
			gc.Warnings = append(gc.Warnings, Warning{Source: string(scope) + " git config", Message: "ignored " + key + ": " + err.Error()})
			continue
		}
		gc.Entries = append(gc.Entries, Entry{Scope: string(scope), Key: nk, Value: val})
	}
	return gc
}

// last returns the final value of key within scope.
func (gc GitConfig) last(scope, key string) (string, bool) {
	var v string
	found := false
	for _, e := range gc.Entries {
		if e.Scope == scope && e.Key == key {
			v, found = e.Value, true
		}
	}
	return v, found
}

// gitBool applies git's boolean spelling: true/yes/on/1, any case.
func gitBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}

// Value returns the final value of key within scope.
func (gc GitConfig) Value(scope, key string) (string, bool) { return gc.last(scope, key) }

// Has reports whether key is set in scope.
func (gc GitConfig) Has(scope, key string) bool {
	_, ok := gc.last(scope, key)
	return ok
}

// OtherScopes lists the scopes other than scope that set key, highest first.
func (gc GitConfig) OtherScopes(scope, key string) []string {
	out := []string{}
	for _, sc := range []string{ScopeCommand, ScopeWorktree, ScopeLocal, ScopeGlobal} {
		if sc != scope && gc.Has(sc, key) {
			out = append(out, sc)
		}
	}
	return out
}
