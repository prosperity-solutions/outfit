package config

import (
	"bytes"
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
	// WorktreeConfig reports extensions.worktreeConfig=true.
	WorktreeConfig bool
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
		lk := strings.ToLower(key)
		if lk == "extensions.worktreeconfig" {
			gc.WorktreeConfig = val == "true" || val == "on" || val == "yes" || val == "1"
			continue
		}
		name, ok := strings.CutPrefix(key, "outfit.")
		if !ok {
			// git lowercases section names, so "Outfit.x" arrives as "outfit.x".
			continue
		}
		nk, err := NormalizeKey(name)
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
