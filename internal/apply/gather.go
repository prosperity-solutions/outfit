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
	pathpkg "path"
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
	// AuthorProfiles are committed profiles kept on disk one by one (ignored while Author is on).
	AuthorProfiles []string
	// ReplaceDirs are the directories the active profile replaces as a whole
	// (its config's `[outfit "profile"] replace` lines); gather cleans and checks them.
	ReplaceDirs []string
	// OperationDone says the git operation that makes the worktree look busy has just
	// finished: the post-rewrite hook of a rebase runs while git's rebase directory is
	// still there, but nothing can abort it any more.
	OperationDone bool
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
	facts.SameProfile = in.Profile != nil && st.Profile == in.Profile.Name
	if !in.OperationDone {
		facts.GitBusy = gitBusy(in.W.GitDir)
	}

	var candidates []string
	if in.Profile != nil {
		seen := map[string]bool{}
		for _, f := range in.Files {
			if profiles.IsJunk(pathpkg.Base(f)) {
				continue // Finder, editor and merge leftovers are not profile files
			}
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
		facts.Recorded[s.Path] = plan.Record{Target: s.Target, VariantHash: s.VariantHash, VariantBlob: s.VariantBlob, BaseBlob: s.BaseBlob, SeenBase: s.SeenBase, KnownBases: s.KnownBases}
	}

	// Which of those paths does git know, and in what state?
	all := append([]string(nil), candidates...)
	for p := range facts.Recorded {
		if !contains(candidates, p) {
			all = append(all, p)
		}
	}
	sort.Strings(all)
	ask := append([]string(nil), all...)
	// A committed profile's variants are asked about too: git's blob for one tells a
	// pull that changed the profile from an edit made through the link.
	variantRel := map[string]string{}
	if in.Profile != nil && in.Profile.Tier == profiles.Committed {
		for _, p := range candidates {
			if rel, err := filepath.Rel(in.W.WorkTree, in.variantPath(p)); err == nil && !strings.HasPrefix(rel, "..") {
				variantRel[p] = filepath.ToSlash(rel)
				ask = append(ask, variantRel[p])
			}
		}
	}
	// The directories the profile replaces: asked about in the same ls-files call (a literal
	// directory pathspec matches everything below it), so the no-op sync stays at its git budget.
	dirs, dirIssues := cleanReplaceDirs(in.ReplaceDirs)
	facts.DirIssues = append(facts.DirIssues, dirIssues...)
	ask = append(ask, dirs...)
	// A `<path>~cruft` symlink (older git, mid-merge) or a temporary link beside the path (the
	// cross-filesystem fallback of a link that a kill cut short) might be ours. Ask the index
	// about them too (the same call): one that git tracks is the repository's own file and is
	// never ours to remove.
	strays := map[string]map[string]string{} // path -> stray link (relative) -> its text
	for _, p := range all {
		if !realParents(in.W.WorkTree, p) {
			continue
		}
		for _, stray := range []string{p + "~cruft", tmpLinkPath(p)} {
			if t, err := os.Readlink(in.abs(stray)); err == nil && t != "" {
				if strays[p] == nil {
					strays[p] = map[string]string{}
				}
				strays[p][stray] = t
				ask = append(ask, stray)
			}
		}
	}
	idx := map[string]indexEntry{}
	if len(all) > 0 || len(dirs) > 0 {
		args := append([]string{"--literal-pathspecs", "ls-files", "-s", "-t", "-z", "--"}, ask...)
		res, err := in.Git.Read(ctx, args...)
		if err != nil {
			return want, facts, g, fmt.Errorf("cannot read the index: %w", err)
		}
		idx = parseLsFiles(res.Stdout)
	}

	// The patterns in force: what is hidden now decides what a change would take off disk.
	curSparse, _ := plan.ParseSparse(sparseNow(in.W.GitDir))
	facts.Dirs = map[string]plan.DirFacts{}
	for _, d := range dirs {
		df := in.dirFacts(ctx, d, idx, st.ReplaceSeen[d], contains(curSparse.HideDirs, d), &facts, g)
		facts.Dirs[d] = df
	}
	// A directory the patterns hide and the profile no longer replaces: files that are not
	// outfit's links on disk below it would be left half-present when git brings the team's back.
	for _, d := range curSparse.HideDirs {
		if !contains(dirs, d) && plan.ValidSwapPath(d) == nil && realParents(in.W.WorkTree, d+"/x") {
			if facts.UnhideStrays == nil {
				facts.UnhideStrays = map[string][]string{}
			}
			facts.UnhideStrays[d] = in.strayFiles(d)
		}
	}

	var toCheck []string // regular files git has not hidden: is each one untouched?
	for _, p := range all {
		pf := plan.PathFacts{}
		ie := idx[p]
		pf.Tracked, pf.SkipWorktree, pf.Unmerged, pf.Mode = ie.tracked, ie.skip, ie.unmerged, ie.mode
		g.blobs[p] = ie.blob
		pf.BaseBlob = ie.blob

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
		for stray, t := range strays[p] {
			if !idx[stray].tracked && !idx[stray].unmerged && (t == facts.Recorded[p].Target || (in.Profile != nil && t == in.Target(p))) {
				pf.StrayLinks = append(pf.StrayLinks, stray)
			}
		}
		sort.Strings(pf.StrayLinks)
		if in.Profile != nil {
			pf.VariantHash = hashFile(in.variantPath(p))
			if v, ok := variantRel[p]; ok {
				pf.VariantBlob = idx[v].blob
			}
			if vi, err := os.Stat(in.variantPath(p)); err == nil {
				pf.VariantExec = vi.Mode()&0o111 != 0
			}
		}
		facts.Paths[p] = pf
	}
	// (A recorded path the profile no longer lists has no variant; hashFile gives "".)

	// Both the base and the profile's file moved since the last sync: was it ONE commit
	// that touched both (the maintainer adapting the profile)? Only then the swap may
	// stand on the new base without a person looking. Rare, so a git call each is fine.
	for _, p := range candidates {
		rec, ok := facts.Recorded[p]
		pf := facts.Paths[p]
		probe := pf
		probe.SameCommit = true
		if ok && variantRel[p] != "" && plan.Adapted(rec, probe) {
			pf.SameCommit = in.sameCommit(ctx, p, variantRel[p])
			facts.Paths[p] = pf
		}
	}

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
	// A path in the middle of a merge keeps the pattern state it has: git leaves an
	// unmerged file in place whatever the patterns say, and a rewrite that "unhides" it
	// would only make git complain.
	wasHidden := map[string]bool{}
	for _, h := range curSparse.Hidden {
		wasHidden[h] = true
	}
	for _, p := range candidates {
		pf := facts.Paths[p]
		want.Swaps = append(want.Swaps, plan.Swap{Path: p, Target: in.Target(p)})
		if pf.Unmerged && wasHidden[p] {
			hidden = append(hidden, p)
			continue
		}
		if pf.Tracked && !pf.Unmerged && pf.Mode != "120000" && pf.VariantHash != "" && (pf.FS == plan.Missing || pf.FS == plan.Link || pf.FS == plan.File) {
			hidden = append(hidden, p)
		}
	}
	want.ReplaceDirs = dirs
	hide := in.CommittedProfiles && !in.Author
	if len(hidden) > 0 || hide || len(dirs) > 0 {
		var show []string
		if in.Profile != nil && in.Profile.Tier == profiles.Committed {
			show = append(show, in.Profile.Name)
		}
		show = append(show, in.AuthorProfiles...)
		want.Sparse = plan.SparseText(plan.SparseOptions{Hidden: hidden, HideDirs: keepHidden(dirs, facts.Dirs), HideProfiles: hide, Show: show})
		want.HidesProfiles = hide
		if hide && want.Sparse != sparseNow(in.W.GitDir) {
			// Only when the patterns are about to change: a profile file that is staged but
			// not in HEAD is then about to leave the disk, and the index holds the only
			// copy. `git diff --cached` is the index against HEAD.
			res, err := in.Git.Read(ctx, "--literal-pathspecs", "diff", "--cached", "--name-only", "-z", "--", profiles.CommittedRoot)
			if err != nil {
				return want, facts, g, fmt.Errorf("cannot list staged profile files: %w", err)
			}
			for _, p := range strings.Split(string(res.Stdout), "\x00") {
				// A shown profile (the active one, or kept by `outfit author on`) stays on disk:
				// only a staged file under a profile that is about to go is at risk.
				rest, _ := strings.CutPrefix(p, profiles.CommittedRoot+"/")
				if name, _, _ := strings.Cut(rest, "/"); p != "" && !contains(show, name) {
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

// sameCommit reports whether the latest commit that touched base also touched variant.
func (in Input) sameCommit(ctx context.Context, base, variant string) bool {
	res, err := in.Git.Read(ctx, "--literal-pathspecs", "log", "-1", "--format=%H", "--", base)
	if err != nil {
		return false
	}
	c := strings.TrimSpace(string(res.Stdout))
	if c == "" {
		return false
	}
	res, err = in.Git.Read(ctx, "--literal-pathspecs", "diff-tree", "--no-commit-id", "--name-only", "-r", "--no-renames", c, "--", variant)
	return err == nil && strings.TrimSpace(string(res.Stdout)) != ""
}

// gitBusy names the git operation in progress in the worktree whose git dir this is.
func gitBusy(gitDir string) string {
	for _, probe := range []struct{ file, name string }{
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"MERGE_HEAD", "merge"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"},
	} {
		if _, err := os.Lstat(filepath.Join(gitDir, probe.file)); err == nil {
			return probe.name
		}
	}
	return ""
}

// sparseNow is the current info/sparse-checkout text ("" when absent).
func sparseNow(gitDir string) string {
	b, err := readInside(gitDir, "info/sparse-checkout")
	if err != nil {
		return ""
	}
	return string(b)
}

// readInside reads rel below dir and can never leave it (os.Root refuses ".." and
// links that point outside).
func readInside(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(rel)
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

// cleanReplaceDirs sorts and validates the declared directories: each a clean relative path outside
// .git and .outfit, none inside another. What cannot be honoured comes back as an issue.
func cleanReplaceDirs(declared []string) (dirs, issues []string) {
	seen := map[string]bool{}
	for _, d := range declared {
		d = strings.TrimSuffix(strings.TrimSpace(d), "/")
		if err := plan.ValidSwapPath(d); err != nil {
			issues = append(issues, "replace = "+d+": "+err.Error())
			continue
		}
		if !seen[d] {
			seen[d] = true
			dirs = append(dirs, d)
		}
	}
	sort.Strings(dirs)
	var out []string
	for i, d := range dirs {
		if i > 0 && strings.HasPrefix(d, dirs[i-1]+"/") {
			issues = append(issues, fmt.Sprintf("replace = %s is inside replace = %s: list only the outer directory", d, dirs[i-1]))
			continue
		}
		out = append(out, d)
	}
	return out, issues
}

// keepHidden lists the directories the patterns should hide: all of them, except one in the middle of
// a merge that is not hidden yet (git leaves unmerged files where they are; hiding would only make it
// complain, and the merge's result is looked at once it is over).
func keepHidden(dirs []string, facts map[string]plan.DirFacts) []string {
	var out []string
	for _, d := range dirs {
		if df := facts[d]; !df.Unmerged || df.Hidden {
			out = append(out, d)
		}
	}
	return out
}

// dirFacts reads what the index says about one replaced directory (from the ls-files answer in idx)
// and, when the directory is about to be hidden, what hiding it would take off disk.
func (in Input) dirFacts(ctx context.Context, d string, idx map[string]indexEntry, seen string, hiddenNow bool, facts *plan.Facts, g *gathered) plan.DirFacts {
	df := plan.DirFacts{Seen: seen, Hidden: hiddenNow}
	if e, ok := idx[d]; ok && e.tracked {
		facts.DirIssues = append(facts.DirIssues, "replace = "+d+": the repository tracks "+d+" as a file or link, not a directory")
		return df
	}
	prefix := d + "/"
	for p, e := range idx {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		if e.unmerged {
			df.Unmerged = true
			continue
		}
		if !e.tracked {
			continue
		}
		if e.mode == "160000" {
			facts.DirIssues = append(facts.DirIssues, "replace = "+d+": "+p+" is a submodule; outfit does not replace a directory that holds one")
			continue
		}
		if hiddenNow && !e.skip {
			df.Visible = append(df.Visible, p)
		}
	}
	df.Tracked, df.Digest = dirDigest(d, idx)
	if df.Tracked > maxSwaps {
		facts.DirIssues = append(facts.DirIssues, fmt.Sprintf("replace = %s: %d tracked files below it (outfit replaces at most %d)", d, df.Tracked, maxSwaps))
	}
	sort.Strings(df.Visible)
	if !hiddenNow && !df.Unmerged && df.Tracked > 0 {
		// About to be hidden: staged work below it has no other copy, and uncommitted edits
		// would leave the switch half done (rows 35 and 40 of docs/git-behavior.md).
		if res, err := in.Git.Read(ctx, "--literal-pathspecs", "diff", "--cached", "--name-only", "-z", "--", d); err == nil {
			for _, p := range strings.Split(string(res.Stdout), "\x00") {
				if p != "" {
					df.Staged = append(df.Staged, p)
				}
			}
		}
		if dirty, err := in.dirtyUnder(ctx, d); err == nil {
			df.Dirty = dirty
		}
	}
	return df
}

// dirtyUnder lists tracked files below d with uncommitted changes.
func (in Input) dirtyUnder(ctx context.Context, d string) ([]string, error) {
	res, err := in.Git.Read(ctx, "--literal-pathspecs", "status", "--porcelain=v1", "-z", "--untracked-files=no", "--no-renames", "--", d)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		if len(rec) > 3 {
			out = append(out, rec[3:])
		}
	}
	sort.Strings(out)
	return out, nil
}

// strayFiles lists regular files on disk below d (never through a link), at most a handful: they are
// what a hidden directory must not hold when it stops being hidden.
func (in Input) strayFiles(d string) []string { return strayFilesIn(in.W.WorkTree, d) }

// strayFilesIn is strayFiles for a worktree path.
func strayFilesIn(worktree, d string) []string {
	var out []string
	_ = filepath.WalkDir(filepath.Join(worktree, filepath.FromSlash(d)), func(path string, e fs.DirEntry, err error) error {
		if err != nil || len(out) >= 20 {
			return nil
		}
		if e.Type().IsRegular() {
			if rel, rerr := filepath.Rel(worktree, path); rerr == nil {
				out = append(out, filepath.ToSlash(rel))
			}
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// dirDigest counts the tracked files below d in an ls-files answer and digests their paths and blobs:
// what "the team changed files below the replaced directory" is measured with.
func dirDigest(d string, idx map[string]indexEntry) (tracked int, digest string) {
	var lines []string
	for p, e := range idx {
		if strings.HasPrefix(p, d+"/") && e.tracked && e.mode != "160000" {
			lines = append(lines, p+" "+e.blob)
		}
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return len(lines), hex.EncodeToString(sum[:])
}
