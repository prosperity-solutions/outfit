package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/profiles"
)

type lsEntry struct {
	Name   string   `json:"name"`
	Tier   string   `json:"tier"`
	Active bool     `json:"active"`
	Keys   []string `json:"keys"`
	Files  []string `json:"files"`
}

// cmdLs lists the profiles across the three tiers.
func cmdLs(ctx context.Context, e Env, args []string) int {
	fs := newFlags("ls")
	asJSON := fs.Bool("json", false, "print machine-readable output")
	pos, code, stop := parseArgs(e, fs, args)
	if stop {
		return code
	}
	if code, bad := noArgs(e, "ls", pos, args); bad {
		return code
	}
	l, err := load(ctx, e)
	if err != nil {
		return report(e.Stdout, e.Stderr, *asJSON, err)
	}
	listing := profiles.List(ctx, l.Git, l.Repo)

	// base is built in and always first: "no swaps", what a non-user gets.
	entries := []lsEntry{{Name: config.Base, Tier: "builtin", Active: l.Result.Profile.Name == config.Base || l.Profile == nil, Keys: []string{}, Files: []string{}}}
	var inactiveWarnings []config.Warning
	for _, p := range listing.Profiles {
		ent := lsEntry{Name: p.Name, Tier: string(p.Tier), Active: p.Name == l.Result.Profile.Name, Keys: []string{}, Files: []string{}}
		var cfg *config.File
		if p.Tier == profiles.Committed && !profiles.RealDir(p.Dir) {
			// Hidden from disk (inactive): read it as committed at HEAD.
			cfg = profiles.HeadConfig(ctx, l.Git, p.Name)
			ent.Files = profiles.HeadFiles(ctx, l.Git, p.Name)
		} else {
			cfg, _ = profiles.ReadConfig(p)
			ent.Files = profiles.Files(p)
		}
		if cfg != nil {
			for k, v := range cfg.Keys {
				ent.Keys = append(ent.Keys, k+"="+v)
			}
			sort.Strings(ent.Keys)
			// A broken or malicious inactive profile is worth knowing about
			// before somebody switches to it.
			if !ent.Active { // the active profile's warnings are already in the Result
				inactiveWarnings = append(inactiveWarnings, cfg.Warnings...)
			}
		}
		if ent.Files == nil {
			ent.Files = []string{}
		}
		entries = append(entries, ent)
	}
	shadowed := []lsEntry{}
	for _, p := range listing.Shadowed {
		ent := lsEntry{Name: p.Name, Tier: string(p.Tier), Keys: []string{}, Files: nonNilStrings(profiles.Files(p))}
		if cfg, _ := profiles.ReadConfig(p); cfg != nil {
			for k, v := range cfg.Keys {
				ent.Keys = append(ent.Keys, k+"="+v)
			}
			sort.Strings(ent.Keys)
		}
		shadowed = append(shadowed, ent)
	}
	warnings := append(append(listing.Warnings, l.Result.Warnings...), inactiveWarnings...)

	if *asJSON {
		out, _ := json.Marshal(struct {
			Active   string    `json:"active"`
			Profiles []lsEntry `json:"profiles"`
			// Shadowed are personal profiles a committed one of the same name hides
			// (`outfit show <tier>:<name>`, `outfit new <other> --from <tier>:<name>`).
			Shadowed []lsEntry        `json:"shadowed"`
			Warnings []config.Warning `json:"warnings"`
		}{l.Result.Profile.Name, entries, shadowed, nonNil(warnings)})
		fmt.Fprintln(e.Stdout, string(out))
		return ExitOK
	}

	tw := tabwriter.NewWriter(e.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTIER\tKEYS\tFILES")
	mark := func(a bool) string {
		if a {
			return "*"
		}
		return ""
	}
	for _, ent := range entries {
		fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\n", ent.Name, mark(ent.Active), ent.Tier, dash(strings.Join(ent.Keys, " ")), dash(strings.Join(ent.Files, " ")))
	}
	_ = tw.Flush()
	fmt.Fprintln(e.Stdout, "* active")
	for _, w := range warnings {
		fmt.Fprintf(e.Stderr, "warning: %s\n", w)
	}
	return ExitOK
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func nonNil(w []config.Warning) []config.Warning {
	if w == nil {
		return []config.Warning{}
	}
	return w
}
