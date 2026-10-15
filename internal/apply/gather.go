// Package apply is sync's I/O half (HANDOVER.md §3.5): it gathers the facts the
// pure planner needs, takes the lock, and carries the planned steps out: symlinks,
// sparse checkout through git, quarantine, state.json. Every write is atomic, and
// nothing here ever overwrites bytes a person wrote.
package apply

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // git's own object id, not a security use
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
	}
	for _, s := range st.Swaps {
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

	for _, p := range all {
		pf := plan.PathFacts{}
		ie := idx[p]
		pf.Tracked, pf.SkipWorktree, pf.Unmerged, pf.Mode = ie.tracked, ie.skip, ie.unmerged, ie.mode
		g.blobs[p] = ie.blob

		fi, err := os.Lstat(in.abs(p))
		switch {
		case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscallENOTDIR):
			pf.FS = plan.Missing
		case err != nil:
			return want, facts, g, err
		case fi.Mode()&os.ModeSymlink != 0:
			pf.FS = plan.Link
			pf.LinkTarget, _ = os.Readlink(in.abs(p))
		case fi.Mode().IsRegular():
			pf.FS = plan.File
			if !ie.skip && ie.tracked {
				pf.MatchesIndex = matchesBlob(in.abs(p), ie.blob)
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
	}
	want.Exclude = true

	facts.SparseOn = in.Cfg.SparseCheckout
	facts.ExpectFiles = in.Cfg.ExpectFiles
	if b, err := os.ReadFile(filepath.Join(in.W.GitDir, "info", "sparse-checkout")); err == nil {
		facts.SparseText = string(b)
	}
	facts.ExcludeHas = excludeHas(filepath.Join(in.W.CommonDir, "info", "exclude"))
	return want, facts, g, nil
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

// matchesBlob reports whether the file's content hashes to the committed blob id:
// an untouched copy of the base file. A repository that uses another object
// format than git's own (a blob id of an unknown length) reports false, which
// errs toward refusing.
func matchesBlob(path, blob string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxFile {
		return false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return blobID(b, len(blob)) == blob
}

// blobID is git's object id of a blob: hash("blob <len>\0" + content).
func blobID(content []byte, hexLen int) string {
	header := fmt.Sprintf("blob %d\x00", len(content))
	switch hexLen {
	case 40:
		h := sha1.New() //nolint:gosec // git's object id
		h.Write([]byte(header))
		h.Write(content)
		return hex.EncodeToString(h.Sum(nil))
	case 64:
		h := sha256.New()
		h.Write([]byte(header))
		h.Write(content)
		return hex.EncodeToString(h.Sum(nil))
	}
	return ""
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
