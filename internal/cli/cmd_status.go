package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/profiles"
)

type statusFile struct {
	Path    string `json:"path"`
	Profile string `json:"profile"`
	Tier    string `json:"tier"`
}

type statusJSON struct {
	Profile config.ProfileChoice `json:"profile"`
	// Tier is the active profile's tier ("" for base or a missing profile).
	Tier     string           `json:"tier"`
	Toggles  []config.Toggle  `json:"toggles"`
	Locked   []string         `json:"locked"`
	Files    []statusFile     `json:"files"`
	Warnings []config.Warning `json:"warnings"`
}

// cmdStatus shows the active profile and where every value comes from. It is
// one of the commands that enforce the git floor (init, use and status; version
// only warns), and it never runs inside a hook.
func cmdStatus(ctx context.Context, e Env, args []string) int {
	fs := newFlags("status")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "status", pos, args); bad {
		return code
	}
	fail := func(err error) int { return report(e.Stdout, e.Stderr, *asJSON, err) }

	if _, err := (gitx.Client{Dir: e.Dir}).RequireFloor(ctx); err != nil {
		return fail(Errorf("%v", err))
	}
	l, err := load(ctx, e)
	if err != nil {
		return fail(err)
	}
	res := l.Result

	st := statusJSON{Profile: res.Profile, Locked: nonNilStrings(res.Locked), Files: []statusFile{}, Warnings: nonNil(res.Warnings)}
	for _, k := range res.SortedKeys() {
		st.Toggles = append(st.Toggles, res.Toggles[k])
	}
	// A locked key the team never gave a value still deserves a line: personal
	// overrides of it are ignored, and it stays unset.
	for _, k := range res.Locked {
		if _, ok := res.Toggles[k]; !ok {
			st.Toggles = append(st.Toggles, config.Toggle{Key: k, Source: config.SourceTeam, Locked: true, Unset: true, Shadowed: []config.Candidate{}})
		}
	}
	sort.Slice(st.Toggles, func(i, j int) bool { return st.Toggles[i].Key < st.Toggles[j].Key })
	if st.Toggles == nil {
		st.Toggles = []config.Toggle{}
	}
	if l.Profile != nil {
		st.Tier = string(l.Profile.Tier)
		for _, f := range profiles.Files(*l.Profile) {
			st.Files = append(st.Files, statusFile{Path: f, Profile: l.Profile.Name, Tier: st.Tier})
		}
	}
	// A clash between a personal and a committed profile name is worth saying.
	st.Warnings = append(st.Warnings, profiles.List(ctx, l.Git, l.Repo).Warnings...)

	if *asJSON {
		out, _ := json.Marshal(st)
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}

	w := e.Stdout
	line := esc(res.Profile.Name)
	var notes []string
	if res.Profile.Name != config.Base && l.Profile == nil {
		notes = append(notes, "not found: behaving as base")
	}
	if st.Tier != "" {
		notes = append(notes, st.Tier)
	}
	switch res.Profile.Source {
	case config.SourceWorktree:
		notes = append(notes, "chosen for this worktree")
	case config.SourceLocal:
		notes = append(notes, "chosen for this clone")
	}
	if res.Profile.CloneDefault != "" {
		notes = append(notes, "clone default: "+res.Profile.CloneDefault)
	}
	if len(notes) > 0 {
		line += " (" + strings.Join(notes, "; ") + ")"
	}
	fmt.Fprintf(w, "profile  %s\n", line)

	if len(st.Toggles) == 0 {
		fmt.Fprintln(w, "toggles  (none)")
	}
	for i, t := range st.Toggles {
		label := "         "
		if i == 0 {
			label = "toggles  "
		}
		src := t.Source
		if t.Locked {
			src = "locked by team"
		}
		val := show(t.Value)
		if t.Unset {
			val = "(unset)"
		}
		fmt.Fprintf(w, "%s%s=%s (%s)\n", label, t.Key, val, src)
	}
	if len(st.Files) == 0 {
		fmt.Fprintln(w, "files    (no swaps)")
	}
	for i, f := range st.Files {
		label := "         "
		if i == 0 {
			label = "files    "
		}
		fmt.Fprintf(w, "%s%s ← %s (%s)\n", label, f.Path, f.Profile, f.Tier)
	}
	for _, wn := range st.Warnings {
		fmt.Fprintf(e.Stderr, "warning: %s\n", wn)
	}
	return ExitOK
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// esc makes untrusted text safe to print: no raw control characters.
func esc(s string) string { return strings.Trim(strconv.Quote(s), `"`) }

// show prints a value verbatim when it is plain, quoted otherwise, so control
// characters in a value can never reach the terminal.
func show(v string) string {
	if q := strconv.Quote(v); v != "" && !strings.ContainsAny(v, " \"\\") && q == `"`+v+`"` {
		return v
	}
	return strconv.Quote(v)
}
