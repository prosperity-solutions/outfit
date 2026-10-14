package cli

import (
	"context"
	"encoding/json"
	"flag"
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
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	fs.SetOutput(e.Stderr)
	asJSON := fs.Bool("json", false, "print machine-readable output")
	if _, err := parse(fs, args); err != nil {
		return ExitUsage
	}
	l, err := load(ctx, e)
	if err != nil {
		return report(e.Stdout, e.Stderr, *asJSON, err)
	}
	listing := profiles.List(ctx, l.Git, l.Repo)

	entries := []lsEntry{}
	for _, p := range listing.Profiles {
		ent := lsEntry{Name: p.Name, Tier: string(p.Tier), Active: p.Name == l.Result.Profile.Name, Keys: []string{}, Files: []string{}}
		var cfg *config.File
		if p.Tier == profiles.Committed && !exists(p.Dir) {
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
		}
		if ent.Files == nil {
			ent.Files = []string{}
		}
		entries = append(entries, ent)
	}
	warnings := append(listing.Warnings, l.Result.Warnings...)

	if *asJSON {
		out, _ := json.Marshal(struct {
			Active   string           `json:"active"`
			Profiles []lsEntry        `json:"profiles"`
			Warnings []config.Warning `json:"warnings"`
		}{l.Result.Profile.Name, entries, nonNil(warnings)})
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
	baseMark := mark(l.Result.Profile.Name == config.Base)
	fmt.Fprintf(tw, "%s%s\t%s\t%s\t%s\n", config.Base, baseMark, "-", "-", "-")
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
