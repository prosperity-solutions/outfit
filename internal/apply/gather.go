// Package apply is sync's I/O half (HANDOVER.md §3.5): it gathers the facts the
// pure planner needs, takes the lock, and carries the planned steps out: symlinks,
// sparse checkout through git, quarantine, state.json. Every write is atomic, and
// nothing here ever overwrites bytes a person wrote.
package apply

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/profiles"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// Workspace says where one worktree lives.
type Workspace struct {
	WorkTree  string
	GitDir    string // this worktree's own git dir
	CommonDir string
}

// Input is everything sync needs to know from the caller.
type Input struct {
	W   Workspace
	Git gitx.Client
	Cfg config.GitConfig
	// Profile is the active profile (nil for base).
	Profile *profiles.Profile
	// Files are the profile's swap files, relative with forward slashes. The caller
	// reads them from disk, or from HEAD right after a switch (a hidden profile's
	// files are not on disk yet).
	Files []string
	// CommittedProfiles says the repository has committed profiles, so inactive ones
	// are hidden even on base.
	CommittedProfiles bool
	// Author is author mode: every committed profile stays on disk.
	Author bool
	// Now is the clock for quarantine directory names (tests set it).
	Now func() time.Time
}

// maxFile bounds what absorb and quarantine will read into memory.
const maxFile = 32 << 20

// maxSwaps bounds how many files one profile may swap. Every one becomes a git
// pathspec and a lstat on every sync, and sync runs after every checkout: a
// committed profile with a million files must not be able to stall them.
const maxSwaps = 2000

// realParents reports whether every directory above rel (below root) is a real
// directory: not a symlink, not a file. lstat follows symlinks in parent
// components, so without this a link, a removal or a copy aimed at "d/f" would land
// wherever a committed "d -> /elsewhere" points.
func realParents(root, rel string) bool {
	parts := strings.Split(rel, "/")
	cur := root
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return true // not there yet: MkdirAll will make real directories
		}
		if err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// gathered is what gather learned beyond the planner's Facts.
type gathered struct {
	blobs    map[string]string // path -> committed blob id
	messages []string          // notes about paths that could not be swapped
}

func (in Input) variantPath(p string) string {
	return filepath.Join(in.Profile.Dir, filepath.FromSlash(p))
}

func (in Input) abs(p string) string { return filepath.Join(in.W.WorkTree, filepath.FromSlash(p)) }

// Target is the symlink text for path p: relative into .outfit/profiles for a
// committed profile (edits show in plain `git status`), absolute into the git dir
// for local and worktree profiles.
func (in Input) Target(p string) string {
	if in.Profile.Tier != profiles.Committed {
		return filepath.Join(in.Profile.Dir, filepath.FromSlash(p))
	}
	rel, err := filepath.Rel(filepath.Dir(in.abs(p)), in.variantPath(p))
	if err != nil {
		return in.variantPath(p)
	}
	return filepath.ToSlash(rel)
}

func (in Input) gather(ctx context.Context, st *state.State) (plan.Want, plan.Facts, *gathered, error) {
	g := &gathered{blobs: map[string]string{}}
	var want plan.Want
	facts := plan.Facts{Paths: map[string]plan.PathFacts{}, Recorded: map[string]plan.Record{}}

	var candidates []string
	if in.Profile != nil {
		seen := map[string]bool{}
		for _, f := range in.Files {
			if err := plan.ValidSwapPath(f); err != nil {
				g.messages = append(g.messages, "profile file skipped: "+err.Error())
				continue
			}
			if !seen[f] {
				seen[f] = true
				candidates = append(candidates, f)
			}
		}
		sort.Strings(candidates)
		if len(candidates) > maxSwaps {
			g.messages = append(g.messages, fmt.Sprintf("profile %q lists %d files to swap; outfit swaps at most %d, so none were swapped", in.Profile.Name, len(candidates), maxSwaps))
			candidates = nil
		}
	}
	for _, s := range st.Swaps {
		// state.json is local, but what it names is acted on (links removed, files
		// quarantined): hold it to the same rules as a profile's own paths.
		if err := plan.ValidSwapPath(s.Path); err != nil {
			g.messages = append(g.messages, "ignored a recorded swap: "+err.Error())
			continue
		}
		facts.Recorded[s.Path] = plan.Record{Target: s.Target, VariantHash: s.VariantHash}
	}

	// Which of those paths does git know, and in what state?
	all := append([]string(nil), candidates...)
	for p := range facts.Recorded {
		if !contains(candidates, p) {
			all = append(all, p)
		}
	}
	sort.Strings(all)
	idx := map[string]indexEntry{}
	if len(all) > 0 {
		args := append([]string{"--literal-pathspecs", "ls-files", "-s", "-t", "-z", "--"}, all...)
		res, err := in.Git.Read(ctx, args...)
		if err != nil {
			return want, facts, g, fmt.Errorf("cannot read the index: %w", err)
		}
		idx = parseLsFiles(res.Stdout)
	}

	var toCheck []string // regular files git has not hidden: is each one untouched?
	for _, p := range all {
		pf := plan.PathFacts{}
		ie := idx[p]
		pf.Tracked, pf.SkipWorktree, pf.Unmerged, pf.Mode = ie.tracked, ie.skip, ie.unmerged, ie.mode
		g.blobs[p] = ie.blob

		fi, err := os.Lstat(in.abs(p))
		switch {
		case !realParents(in.W.WorkTree, p):
			pf.FS = plan.Other // a directory above it is a link: never act through it
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscallENOTDIR):
			pf.FS = plan.Missing
		case err != nil:
			return want, facts, g, err
		case fi.Mode()&os.ModeSymlink != 0:
			pf.FS = plan.Link
			pf.LinkTarget, _ = os.Readlink(in.abs(p))
		case fi.Mode().IsRegular():
			pf.FS = plan.File
			if ie.skip {
				pf.FileHash = hashFile(in.abs(p))
			} else if ie.tracked {
				toCheck = append(toCheck, p)
			}
		case fi.IsDir():
			pf.FS = plan.Dir
		default:
			pf.FS = plan.Other
		}
		if in.Profile != nil {
			pf.VariantHash = hashFile(in.variantPath(p))
		}
		facts.Paths[p] = pf
	}
	// A recorded path the profile no longer lists has no variant to hash; its facts
	// only matter for the link/file kind.

	// "Untouched" is decided by git, which applies .gitattributes, autocrlf and
	// clean/smudge filters and notices a mode change: raw bytes cannot.
	if len(toCheck) > 0 {
		dirty, err := in.dirty(ctx, toCheck)
		if err != nil {
			return want, facts, g, err
		}
		for _, p := range toCheck {
			pf := facts.Paths[p]
			pf.MatchesIndex = !dirty[p]
			facts.Paths[p] = pf
		}
	}

	var hidden []string
	for _, p := range candidates {
		pf := facts.Paths[p]
		want.Swaps = append(want.Swaps, plan.Swap{Path: p, Target: in.Target(p)})
		if pf.Tracked && !pf.Unmerged && pf.Mode != "120000" && pf.VariantHash != "" && (pf.FS == plan.Missing || pf.FS == plan.Link || pf.FS == plan.File) {
			hidden = append(hidden, p)
		}
	}
	hide := in.CommittedProfiles && !in.Author
	if len(hidden) > 0 || hide {
		active := ""
		if in.Profile != nil && in.Profile.Tier == profiles.Committed {
			active = in.Profile.Name
		}
		want.Sparse = plan.SparseText(plan.SparseOptions{Hidden: hidden, HideProfiles: hide, Active: active})
		want.HidesProfiles = hide
		if hide && want.Sparse != sparseNow(in.W.GitDir) {
			// Only when the patterns are about to change. Compared with nothing but the index: a staged-only profile file is about
			// to be taken off disk, and nothing else holds a copy.
			res, err := in.Git.Read(ctx, "--literal-pathspecs", "diff", "--cached", "--name-only", "-z", "--", profiles.CommittedRoot)
			if err != nil {
				return want, facts, g, fmt.Errorf("cannot list staged profile files: %w", err)
			}
			for _, p := range strings.Split(string(res.Stdout), "\x00") {
				if p != "" {
					facts.StagedProfiles = append(facts.StagedProfiles, p)
				}
			}
		}
	}
	want.Exclude = true

	facts.SparseOn = in.Cfg.SparseCheckout
	facts.ExpectFiles = in.Cfg.ExpectFiles
	facts.SparseText = sparseNow(in.W.GitDir)
	facts.ExcludeHas = excludeHas(filepath.Join(in.W.CommonDir, "info", "exclude"))
	return want, facts, g, nil
}

// sparseNow is the current info/sparse-checkout text ("" when absent).
func sparseNow(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "info", "sparse-checkout"))
	if err != nil {
		return ""
	}
	return string(b)
}

// dirty asks git which of paths differ from the index (content after filters,
// or mode). Paths git does not list are clean.
func (in Input) dirty(ctx context.Context, paths []string) (map[string]bool, error) {
	args := append([]string{"--literal-pathspecs", "status", "--porcelain=v1", "-z", "--untracked-files=no", "--no-renames", "--"}, paths...)
	res, err := in.Git.Read(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("cannot ask git whether %d file(s) have uncommitted changes: %w", len(paths), err)
	}
	out := map[string]bool{}
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		if len(rec) > 3 {
			out[rec[3:]] = true
		}
	}
	return out, nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// indexEntry is one `git ls-files -s -t -z` record.
type indexEntry struct {
	tracked, skip, unmerged bool
	mode, blob              string
}

// parseLsFiles reads "<tag> <mode> <blob> <stage>\t<path>\0" records. A path with
// any stage other than 0 is unmerged.
func parseLsFiles(b []byte) map[string]indexEntry {
	out := map[string]indexEntry{}
	for _, rec := range bytes.Split(b, []byte{0}) {
		meta, path, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) < 4 {
			continue
		}
		e := out[path]
		if f[3] != "0" {
			e.unmerged = true
			out[path] = e
			continue
		}
		e.tracked, e.mode, e.blob = true, f[1], f[2]
		e.skip = f[0] == "S"
		out[path] = e
	}
	return out
}

// hashFile is the SHA-256 of a regular file's content, "" when it is missing, a
// link, or not readable: a profile's variant must be a plain file.
func hashFile(path string) string {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFile {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// excludeEntry is what outfit adds to .git/info/exclude.
const (
	excludeComment = "# outfit: keep search tools out of the profile files (outfit uninstall removes this)"
	excludeLine    = "/.outfit/"
)

func excludeHas(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == excludeLine {
			return true
		}
	}
	return false
}
