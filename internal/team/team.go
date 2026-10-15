// Package team writes what `outfit init --team` adds for the people of a repository who do not use
// outfit yet (docs/HANDOVER.md §3.0): one line in the base AGENTS.md that says the repository supports
// outfit profiles, and docs/outfit.md that says how to install it and how a hook reads a toggle with or
// without it. Nothing is committed and nothing else changes for non-users.
package team

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	Link    = "link"    // AGENTS.md: a link the team committed that leads nowhere outfit may write
	Created = "created" // docs/outfit.md: written
	Exists  = "exists"  // docs/outfit.md: there already, left exactly as it is
)

// Result is what init --team did (or, with dry, would do).
type Result struct {
	AgentsMD string `json:"agents_md"`
	Doc      string `json:"doc,omitempty"`
	DryRun   bool   `json:"dry_run,omitempty"`
}

// Doc is the content of docs/outfit.md.
const Doc = `# Using outfit in this repository (optional)

[outfit](https://github.com/prosperity-solutions/outfit) lets you run this repository your own way
(your own ` + "`AGENTS.md`" + `, your own on/off switches for checks) without changing anything for the team and
without a dirty ` + "`git status`" + `. **You do not need it:** without outfit, you get exactly what is committed.

## Install

One line, no login and no token:

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

// Apply adds the line and the document under root (the worktree), never through a link that leads
// outside it, never replacing anything that is there. swapped says outfit has swapped AGENTS.md (a
// profile's link stands there, or the base file is hidden for it): then nothing at all is written,
// because the team's file is not the one in the tree and half of the job would leave a repository that
// refers to a document that is not there. dry only reports. Every check runs before the first write, and
// the document is written before the line that points to it.
func Apply(root string, dry, swapped bool) (Result, error) {
	r := Result{DryRun: dry}
	if swapped {
		r.AgentsMD = Swapped
		return r, nil
	}
	rt, err := os.OpenRoot(root)
	if err != nil {
		return r, err
	}
	defer func() { _ = rt.Close() }()

	// 1. What there is: AGENTS.md (itself, or what a team link of it leads to) and docs/outfit.md.
	target := AgentsPath
	var agentsText string
	var agentsPerm fs.FileMode = 0o644
	haveAgents := false
	fi, err := rt.Lstat(AgentsPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		r.AgentsMD = Missing
	case err != nil:
		return r, fmt.Errorf("%s: %w", AgentsPath, err)
	default:
		if fi.Mode()&fs.ModeSymlink != 0 {
			// A link the team committed (AGENTS.md -> CLAUDE.md). Follow it while it stays inside the
			// worktree, outside .outfit/ (a profile's file is not the team's), and ends at a regular file.
			resolved, rerr := resolveInside(root, AgentsPath)
			if rerr != nil {
				r.AgentsMD = Link
			} else {
				target = resolved
				fi, err = rt.Lstat(target)
				if err != nil || !fi.Mode().IsRegular() {
					r.AgentsMD = Link
				}
			}
		}
		if r.AgentsMD == "" {
			if !fi.Mode().IsRegular() {
				return r, fmt.Errorf("%s is not a regular file", AgentsPath)
			}
			b, rerr := rt.ReadFile(target)
			if rerr != nil {
				return r, fmt.Errorf("%s: %w", target, rerr)
			}
			agentsText, agentsPerm, haveAgents = string(b), fi.Mode().Perm(), true
			if hasLine(agentsText) {
				r.AgentsMD = Present
			} else {
				r.AgentsMD = Added
			}
		}
	}
	if di, derr := rt.Lstat("docs"); derr == nil && di.Mode()&fs.ModeSymlink != 0 {
		return r, errors.New("docs is a link; outfit does not write through it")
	} else if derr == nil && !di.IsDir() {
		return r, errors.New("docs is not a directory")
	}
	dfi, derr := rt.Lstat(DocPath)
	switch {
	case derr == nil && dfi.Mode()&fs.ModeSymlink != 0:
		return r, fmt.Errorf("%s is a link; outfit does not write through it", DocPath)
	case derr == nil && !dfi.Mode().IsRegular():
		return r, fmt.Errorf("%s is not a regular file", DocPath)
	case derr == nil:
		r.Doc = Exists
	case errors.Is(derr, fs.ErrNotExist):
		r.Doc = Created
	default:
		return r, fmt.Errorf("%s: %w", DocPath, derr)
	}
	if dry {
		return r, nil
	}

	// 2. Write: the document first, then the line that points to it.
	if r.Doc == Created {
		if err := rt.MkdirAll("docs", 0o755); err != nil {
			return r, err
		}
		if err := replace(rt, DocPath, Doc, 0o644); err != nil {
			return r, err
		}
	}
	if r.AgentsMD == Added && haveAgents {
		nl := "\n"
		if strings.Contains(agentsText, "\r\n") {
			nl = "\r\n"
		}
		text := agentsText
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += nl
		}
		if text != "" && !strings.HasSuffix(text, nl+nl) {
			text += nl
		}
		if err := replace(rt, target, text+AgentsLine+nl, agentsPerm); err != nil {
			return r, err
		}
	}
	return r, nil
}

// resolveInside follows the links of name (relative to root) and returns the final path relative to
// root, only if every step stays inside root and outside .outfit/ (where profile files live).
func resolveInside(root, name string) (string, error) {
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(filepath.Join(rootReal, name))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootReal, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("leaves the worktree")
	}
	rel = filepath.ToSlash(rel)
	if rel == ".outfit" || strings.HasPrefix(rel, ".outfit/") {
		return "", errors.New("is a profile's file")
	}
	return rel, nil
}

// hasLine reports that AGENTS.md already carries the line, or a version of it the team edited (a list
// item, reworded around the same sentence).
func hasLine(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == AgentsLine || strings.Contains(l, "supports outfit profiles") && strings.Contains(l, DocPath) {
			return true
		}
	}
	return false
}

// replace writes name beside itself (a name no other run uses, created exclusively) and renames it
// into place.
func replace(rt *os.Root, name, content string, perm fs.FileMode) error {
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := name + ".outfit-tmp-" + hex.EncodeToString(rnd[:])
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
