package config

import (
	"fmt"
	"slices"
	"sort"
)

// Base is the profile that means "no swaps": what a non-user gets (§3.0).
const Base = "base"

// Where a value came from, highest precedence first (§3.2). SourceCommand is
// `git -c outfit.x=y …` (and GIT_CONFIG_COUNT), which hooks inherit: the natural
// way to skip a check once. It is not in the spec's list; see docs/config.md.
const (
	SourceCommand  = "command"
	SourceWorktree = "worktree"
	SourceLocal    = "local"
	SourceGlobal   = "global"
	SourceProfile  = "profile"
	SourceTeam     = "team"
)

// Candidate is one place a key is set.
type Candidate struct {
	Source string `json:"source"`
	Value  string `json:"value"`
}

// Toggle is the resolution of one key.
type Toggle struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
	Locked bool   `json:"locked"`
	// Unset marks a locked key the team never gave a value: personal overrides
	// of it are ignored and it stays unset.
	Unset bool `json:"unset,omitempty"`
	// Shadowed lists lower-precedence values that lost (or, for a locked key,
	// the overrides that were ignored).
	Shadowed []Candidate `json:"shadowed,omitempty"`
}

// Input is everything Resolve needs; it does no I/O.
type Input struct {
	Git GitConfig
	// Team is .outfit/config (nil when absent).
	Team *File
	// Profile is the active profile's profile.config (nil for base or when the
	// profile has none).
	Profile *File
}

// ProfileChoice is the active profile and where the choice was made.
type ProfileChoice struct {
	Name string `json:"name"`
	// Source is worktree, local or "default" (nothing set: base).
	Source string `json:"source"`
	// CloneDefault is the --local choice when a worktree override shadows it.
	CloneDefault string `json:"clone_default,omitempty"`
}

// Result is the resolved configuration.
type Result struct {
	Profile  ProfileChoice
	Toggles  map[string]Toggle
	Locked   []string // sorted, from the team file
	Warnings []Warning
}

// ChooseProfile picks the active profile: --worktree, then --local, else base
// (the command scope is not used for the profile: it is a per-call toggle).
// A --global `outfit.profile` is ignored with a warning: profile names are per
// repo, so a machine-wide one is always a mistake.
func ChooseProfile(gc GitConfig) (ProfileChoice, []Warning) {
	var warns []Warning
	if _, ok := gc.last(ScopeGlobal, KeyProfile); ok {
		warns = append(warns, Warning{Source: "global git config", Message: "outfit.profile is set globally and ignored: profile names are per repo (use `outfit use <profile>` inside a repo, then `git config --global --unset outfit.profile`)"})
	}
	wt, hasWT := gc.last(ScopeWorktree, KeyProfile)
	lc, hasLC := gc.last(ScopeLocal, KeyProfile)
	switch {
	case hasWT && wt != "":
		c := ProfileChoice{Name: wt, Source: SourceWorktree}
		if hasLC && lc != wt {
			c.CloneDefault = lc
		}
		return c, warns
	case hasLC && lc != "":
		return ProfileChoice{Name: lc, Source: SourceLocal}, warns
	}
	return ProfileChoice{Name: Base, Source: "default"}, warns
}

// Resolve computes every toggle. Pure.
func Resolve(in Input) Result {
	res := Result{Toggles: map[string]Toggle{}}
	choice, warns := ChooseProfile(in.Git)
	res.Profile = choice
	res.Warnings = append(res.Warnings, warns...)
	res.Warnings = append(res.Warnings, in.Git.Warnings...)

	locked := map[string]bool{}
	if in.Team != nil {
		res.Warnings = append(res.Warnings, in.Team.Warnings...)
		for _, k := range in.Team.Locked {
			locked[k] = true
		}
	}
	if in.Profile != nil {
		res.Warnings = append(res.Warnings, in.Profile.Warnings...)
	}
	for k := range locked {
		res.Locked = append(res.Locked, k)
	}
	sort.Strings(res.Locked)

	// Every key anyone sets.
	keys := map[string]bool{}
	for _, e := range in.Git.Entries {
		keys[e.Key] = true
	}
	if in.Team != nil {
		for k := range in.Team.Keys {
			keys[k] = true
		}
	}
	if in.Profile != nil {
		for k := range in.Profile.Keys {
			keys[k] = true
		}
	}
	for k := range locked {
		keys[k] = true
	}
	delete(keys, KeyProfile)
	delete(keys, KeyLocked)
	for _, sc := range []string{ScopeCommand, ScopeWorktree, ScopeLocal, ScopeGlobal} {
		if _, ok := in.Git.last(sc, KeyLocked); ok {
			res.Warnings = append(res.Warnings, Warning{Source: sc + " git config", Message: "outfit.locked set personally: ignored (only .outfit/config can lock keys)"})
		}
	}

	for k := range keys {
		var cands []Candidate
		for _, sc := range []string{ScopeCommand, ScopeWorktree, ScopeLocal, ScopeGlobal} {
			if v, ok := in.Git.last(sc, k); ok {
				cands = append(cands, Candidate{Source: sc, Value: v})
			}
		}
		if in.Profile != nil {
			if v, ok := in.Profile.Keys[k]; ok {
				cands = append(cands, Candidate{Source: SourceProfile, Value: v})
			}
		}
		var team *Candidate
		if in.Team != nil {
			if v, ok := in.Team.Keys[k]; ok {
				team = &Candidate{Source: SourceTeam, Value: v}
				cands = append(cands, *team)
			}
		}

		if locked[k] {
			// A locked key resolves from .outfit/config alone.
			var ignored []Candidate
			for _, c := range cands {
				if c.Source != SourceTeam {
					ignored = append(ignored, c)
					res.Warnings = append(res.Warnings, Warning{Source: c.Source, Message: fmt.Sprintf("%s=%s ignored: %q is locked by .outfit/config", k, c.Value, k)})
				}
			}
			if team == nil {
				// Locked but the team set no value: nothing resolves.
				continue
			}
			res.Toggles[k] = Toggle{Key: k, Value: team.Value, Source: SourceTeam, Locked: true, Shadowed: ignored}
			continue
		}
		if len(cands) == 0 {
			continue
		}
		res.Toggles[k] = Toggle{Key: k, Value: cands[0].Value, Source: cands[0].Source, Shadowed: slices.Clone(cands[1:])}
	}
	return res
}

// Get returns the resolution of one key.
func (r Result) Get(key string) (Toggle, bool) {
	t, ok := r.Toggles[key]
	return t, ok
}

// SortedKeys returns the resolved keys in stable order.
func (r Result) SortedKeys() []string {
	keys := make([]string, 0, len(r.Toggles))
	for k := range r.Toggles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// IsLocked reports whether key is locked by the team.
func (r Result) IsLocked(key string) bool { return slices.Contains(r.Locked, key) }
