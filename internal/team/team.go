// Package team writes what `outfit init --team` adds for the people of a repository who do not use
// outfit yet (docs/HANDOVER.md §3.0): one line in the base AGENTS.md that says the repository supports
// outfit profiles, and docs/outfit.md that says how to install it and how a hook reads a toggle with or
// without it. Nothing is committed and nothing else changes for non-users.
package team

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// AgentsLine is the one line added to AGENTS.md.
const AgentsLine = "Optional: this repo supports outfit profiles; see docs/outfit.md to install."

// DocPath and AgentsPath are relative to the worktree root.
const (
	DocPath    = "docs/outfit.md"
	AgentsPath = "AGENTS.md"
)

// Outcomes, per file.
const (
	Added   = "added"   // AGENTS.md: the line was appended
	Present = "present" // AGENTS.md: the line is already there
	Missing = "missing" // AGENTS.md: there is none (the line is printed for the person to place)
	Swapped = "swapped" // AGENTS.md: a profile's link stands there; the base file is not reachable
	Created = "created" // docs/outfit.md: written
	Exists  = "exists"  // docs/outfit.md: there already, left exactly as it is
)

// Result is what init --team did (or, with dry, would do).
type Result struct {
	AgentsMD string `json:"agents_md"`
	Doc      string `json:"doc"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// Doc is the content of docs/outfit.md.
const Doc = `# Using outfit in this repository (optional)

[outfit](https://github.com/prosperity-solutions/outfit) lets you run this repository your own way
(your own ` + "`AGENTS.md`" + `, your own on/off switches for checks) without changing anything for the team and
without a dirty ` + "`git status`" + `. **You do not need it:** without outfit, you get exactly what is committed.

## Install

While the outfit repository is private, with a GitHub login that can read it:

` + "```sh" + `
curl -fsSL -H "Authorization: Bearer $(gh auth token)" https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
` + "```" + `

Once it is public (no login needed):

` + "```sh" + `
curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
` + "```" + `

The installer checks the download's SHA-256 and its signature and never uses ` + "`sudo`" + `
(` + "`outfit update`" + ` installs newer releases later).

## Start

` + "```sh" + `
outfit init            # put outfit in front of your git hooks (your team's hooks keep running)
outfit ls              # the profiles this repository offers
outfit use <profile>   # switch; ` + "`outfit use base`" + ` goes back to exactly the committed files
outfit status          # what is active, where each value comes from
outfit skills          # the same documentation, written for coding agents
` + "```" + `

## For hook authors: read a switch with or without outfit

The team's defaults are in ` + "`.outfit/config`" + `. A hook that honours a switch reads it like this; it works
on a machine without outfit too (` + "`git config -f`" + ` reads only that one file and follows nothing):

` + "```sh" + `
v=$(outfit get biome 2>/dev/null || git config -f .outfit/config --get outfit.biome)
[ "$v" = off ] && exit 0
` + "```" + `
`

// Apply adds the line and the document under root (the worktree), never through a link, never replacing
// anything that is there. dry only reports.
func Apply(root string, dry bool) (Result, error) {
	r := Result{DryRun: dry}
	rt, err := os.OpenRoot(root)
	if err != nil {
		return r, err
	}
	defer func() { _ = rt.Close() }()

	// AGENTS.md
	fi, err := rt.Lstat(AgentsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.AgentsMD = Missing
	case err != nil:
		return r, fmt.Errorf("%s: %w", AgentsPath, err)
	case fi.Mode()&fs.ModeSymlink != 0:
		// A swapped path: the link points into a profile. Appending would edit the profile, and
		// writing only half of what was asked would leave a repository that refers to a document
		// that is not there: nothing is written until `outfit use base`.
		r.AgentsMD = Swapped
		return r, nil
	case !fi.Mode().IsRegular():
		return r, fmt.Errorf("%s is not a regular file", AgentsPath)
	default:
		b, rerr := rt.ReadFile(AgentsPath)
		if rerr != nil {
			return r, fmt.Errorf("%s: %w", AgentsPath, rerr)
		}
		if hasLine(string(b)) {
			r.AgentsMD = Present
		} else {
			r.AgentsMD = Added
			if !dry {
				text := string(b)
				if text != "" && !strings.HasSuffix(text, "\n") {
					text += "\n"
				}
				if text != "" && !strings.HasSuffix(text, "\n\n") {
					text += "\n"
				}
				if err := replace(rt, AgentsPath, text+AgentsLine+"\n", fi.Mode().Perm()); err != nil {
					return r, err
				}
			}
		}
	}

	// docs/outfit.md
	dfi, derr := rt.Lstat(DocPath)
	switch {
	case derr == nil && dfi.Mode()&fs.ModeSymlink != 0:
		return r, fmt.Errorf("%s is a link; outfit does not write through it", DocPath)
	case derr == nil:
		r.Doc = Exists
	case errors.Is(derr, fs.ErrNotExist):
		r.Doc = Created
		if !dry {
			if di, e := rt.Lstat("docs"); e == nil && di.Mode()&fs.ModeSymlink != 0 {
				return r, errors.New("docs is a link; outfit does not write through it")
			}
			if err := rt.MkdirAll("docs", 0o755); err != nil {
				return r, err
			}
			if err := replace(rt, DocPath, Doc, 0o644); err != nil {
				return r, err
			}
		}
	default:
		return r, fmt.Errorf("%s: %w", DocPath, derr)
	}
	return r, nil
}

func hasLine(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == AgentsLine {
			return true
		}
	}
	return false
}

// replace writes name beside itself (created exclusively) and renames it into place.
func replace(rt *os.Root, name, content string, perm fs.FileMode) error {
	tmp := name + ".outfit-tmp"
	if err := rt.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	f, err := rt.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, werr := f.WriteString(content)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = rt.Rename(tmp, name)
	}
	if werr != nil {
		_ = rt.Remove(tmp)
	}
	return werr
}
