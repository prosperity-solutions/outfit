// Package config reads outfit's configuration: the committed team files
// (`.outfit/config`, `profile.config`) through a deliberately tiny parser that
// accepts only `outfit.*` keys, and the personal overrides in git config. It
// never `include.path`s a committed file: a committed config could set
// core.fsmonitor, core.pager, core.hooksPath or includeIf, which is code
// execution on every teammate's machine (HANDOVER.md §3.2).
package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Reserved key names (HANDOVER.md §3.1).
const (
	// KeyProfile names the active profile. Never allowed in committed files.
	KeyProfile = "profile"
	// KeyLocked lists the keys the team locks. Only valid in .outfit/config.
	KeyLocked = "locked"
	// HookPrefix prefixes the per-hook toggles: hook.<name>.
	HookPrefix = "hook."
)

// A toggle key is `name` or `hook.name`; names are lowercase, digits and dashes.
//
// `hook.name` is a dotted key. In a committed FILE git itself only accepts it as
// the subsection form `[outfit "hook"] name = …`, but HANDOVER.md §3.1's own
// example writes `hook.pre-commit = off` inside [outfit]. So the parser accepts
// both spellings and WARNS on the dotted one (see ParseFile): rejecting it would
// break the spec's example, accepting it silently would break non-users.
var keyRE = regexp.MustCompile(`^(hook\.)?[a-z][a-z0-9-]*$`)

// NormalizeKey lowercases k and reports whether it is a valid toggle key.
func NormalizeKey(k string) (string, error) {
	n := strings.ToLower(strings.TrimSpace(k))
	if !keyRE.MatchString(n) {
		return "", fmt.Errorf("invalid key %q: use lowercase letters, digits and dashes (hook toggles are hook.<name>)", k)
	}
	return n, nil
}

// IsReserved reports whether k may not be used as a personal toggle through
// `outfit set`: `profile` is set by `outfit use`, `locked` only by the team.
func IsReserved(k string) bool { return k == KeyProfile || k == KeyLocked }

// Warning is a problem worth showing in `status` that never stops a command.
type Warning struct {
	Source  string `json:"source"`         // file path or scope the problem is in
	Line    int    `json:"line,omitempty"` // 1-based, for files
	Message string `json:"message"`
}

func (w Warning) String() string {
	if w.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", w.Source, w.Line, w.Message)
	}
	return fmt.Sprintf("%s: %s", w.Source, w.Message)
}
