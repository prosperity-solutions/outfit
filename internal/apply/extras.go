package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/prosperity-solutions/outfit/internal/fault"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// The profile's own files below a replaced directory ("extras"): paths the repository does not
// track, linked in like swapped files and kept out of git's sight by an untracked `.gitignore` in
// the directory that ignores itself. It is per worktree (git's shared info/exclude would hide a
// file from a worktree that never asked). state.json lists them, but only as a cache: sync and
// uninstall also walk the directories and find every link of outfit's by its text.

// personalLinkDirs are the directories a personal profile's links point into.
func personalLinkDirs(w Workspace) []string {
	return []string{
		filepath.Join(w.CommonDir, "outfit", "profiles") + string(filepath.Separator),
		filepath.Join(w.GitDir, "outfit", "worktree-profiles") + string(filepath.Separator),
	}
}

// isExtraLink reports that text is the link outfit makes for the profile's file rel (relative path
// from the worktree root): to rel inside some profile, not to anything else there. A link a person
// made by hand into a profile directory (an alias for another profile file, a link to its
// config) is theirs.
func isExtraLink(rel, text string, personal []string) bool {
	if strings.HasPrefix(text, "/") {
		return isOurLinkText(text, personal) && strings.HasSuffix(text, "/"+rel)
	}
	head := strings.Repeat("../", strings.Count(rel, "/")) + ".outfit/profiles/"
	mid, ok := strings.CutPrefix(text, head)
	if !ok {
		return false
	}
	name, tail, ok := strings.Cut(mid, "/")
	return ok && name != "" && tail == rel
}

// isOurIgnoreFile reports that the file at rel is an ignore file outfit wrote.
func isOurIgnoreFile(worktree, rel string) bool {
	if !realParents(worktree, rel) {
		return false
	}
	abs := filepath.Join(worktree, filepath.FromSlash(rel))
	if fi, err := os.Lstat(abs); err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return false
	}
	b, err := os.ReadFile(abs)
	return err == nil && plan.OurIgnore(string(b))
}

// reservedExtraName says why git itself must read a file of this name as a plain file, never a link.
func reservedExtraName(p string) bool {
	switch strings.ToLower(pathpkg.Base(p)) {
	case ".gitignore", ".gitattributes", ".gitmodules":
		return true
	}
	return false
}

// extraScanDirs are the directories that may hold links of the profile's own files: the ones the
// profile replaces now, the ones the state says it replaced, and the ones the patterns hide.
func extraScanDirs(w Workspace, st *state.State, declared []string) []string {
	var out []string
	add := func(d string) {
		d = strings.TrimSuffix(strings.TrimSpace(d), "/")
		if d == "" || plan.ValidSwapPath(d) != nil || containsStr(out, d) {
			return
		}
		out = append(out, d)
	}
	for _, d := range declared {
		add(d)
	}
	for _, d := range st.ReplaceDirs {
		add(d)
	}
	if o, ok := plan.ParseSparse(sparseNow(w.GitDir)); ok {
		for _, d := range o.HideDirs {
			add(d)
		}
	}
	sort.Strings(out)
	return out
}

// walkLinks calls fn for every symbolic link below dir (relative paths, slash separated), without
// following any, and stops after maxStrayWalk entries.
func walkLinks(worktree, dir string, fn func(rel, text string)) {
	if !dirExists(filepath.Join(worktree, filepath.FromSlash(dir))) || !realParents(worktree, dir+"/x") {
		return
	}
	n := 0
	_ = filepath.WalkDir(filepath.Join(worktree, filepath.FromSlash(dir)), func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if n++; n > maxStrayWalk {
			return fs.SkipAll
		}
		if e.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		rel, rerr := filepath.Rel(worktree, path)
		if rerr != nil {
			return nil
		}
		if t, terr := os.Readlink(path); terr == nil && t != "" {
			fn(filepath.ToSlash(rel), t)
		}
		return nil
	})
}

// orphanExtras adds to recorded the links of outfit's below the directories that the state does
// not list (a record lost to an older binary, a kill between a link and its record): they are
// removed like any link the profile no longer wants, or kept when it still does.
func (in Input) orphanExtras(dirs []string, recorded map[string]plan.Record) {
	personal := personalLinkDirs(in.W)
	n := 0
	for _, d := range dirs {
		walkLinks(in.W.WorkTree, d, func(rel, text string) {
			if _, ok := recorded[rel]; ok || n >= maxSwaps || !isExtraLink(rel, text, personal) || plan.ValidSwapPath(rel) != nil {
				return
			}
			n++
			recorded[rel] = plan.Record{Target: text, Extra: true}
		})
	}
}

// trackedIdx answers "what does the team track below the replaced directories" for the clash
// checks of an extra.
type trackedIdx struct {
	files, dirs, lower map[string]bool
}

func newTrackedIdx(idx map[string]indexEntry, dirs []string) trackedIdx {
	t := trackedIdx{files: map[string]bool{}, dirs: map[string]bool{}, lower: map[string]bool{}}
	for k, e := range idx {
		if !e.tracked || underDirOf(k, dirs) == "" {
			continue
		}
		t.files[k] = true
		t.lower[strings.ToLower(k)] = true
		for a := pathpkg.Dir(k); a != "." && a != "/"; a = pathpkg.Dir(a) {
			t.dirs[a] = true
		}
	}
	return t
}

func underDirOf(p string, dirs []string) string {
	for _, d := range dirs {
		if strings.HasPrefix(p, d+"/") {
			return d
		}
	}
	return ""
}

// clash says why a path cannot hold a file of the profile's: a file the team tracks is one of its
// parent directories, the team tracks files below it, or git on a case-folding system would take
// it for a tracked file. "" when nothing is in the way.
func (t trackedIdx) clash(p string) string {
	for a := pathpkg.Dir(p); a != "." && a != "/"; a = pathpkg.Dir(a) {
		if t.files[a] {
			return "the team tracks a file at " + a + ", where a directory would have to stand"
		}
	}
	if t.dirs[p] {
		return "the team tracks files below " + p + ", where a file would have to stand"
	}
	if t.lower[strings.ToLower(p)] {
		return "its name differs only in case from a file the team tracks (git on this system could not tell them apart)"
	}
	return ""
}

// ignoreFacts reads `<d>/.gitignore`: what it holds, and whether outfit may write it.
func (in Input) ignoreFacts(d string, idx map[string]indexEntry) plan.IgnoreFacts {
	rel := d + "/.gitignore"
	if idx[rel].tracked || idx[rel].unmerged {
		// (a file of ours that is still there, from before the team tracked the path, is told apart
		// so that it can go: it would otherwise stand at a tracked path for good)
		f := plan.IgnoreFacts{Blocked: "the team tracks " + rel + ", and outfit will not write into it"}
		if isOurIgnoreFile(in.W.WorkTree, rel) {
			if b, err := os.ReadFile(in.abs(rel)); err == nil {
				f.Text = string(b)
			}
		}
		return f
	}
	if !realParents(in.W.WorkTree, rel) {
		return plan.IgnoreFacts{Blocked: d + " is below a symlinked directory"}
	}
	fi, err := os.Lstat(in.abs(rel))
	switch {
	case errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscallENOTDIR):
		return plan.IgnoreFacts{}
	case err != nil || !fi.Mode().IsRegular() || fi.Size() > 1<<20:
		return plan.IgnoreFacts{Blocked: rel + " is not a regular file of outfit's"}
	}
	b, err := os.ReadFile(in.abs(rel))
	if err != nil || !plan.OurIgnore(string(b)) {
		return plan.IgnoreFacts{Blocked: rel + " exists and is not outfit's: it stays as it is (outfit will not link files below " + d + " that it cannot hide from git)"}
	}
	return plan.IgnoreFacts{Text: string(b)}
}

// writeIgnore keeps `<d>/.gitignore` equal to text ("" removes it). Only a file that is outfit's
// (or none) is ever replaced.
func (in Input) writeIgnore(d, text string) error {
	rel := d + "/.gitignore"
	if !realParents(in.W.WorkTree, rel) {
		return fmt.Errorf("%s is below a symlinked directory: refusing to write through it", d)
	}
	abs := in.abs(rel)
	if b, err := os.ReadFile(abs); err == nil && !plan.OurIgnore(string(b)) {
		return fmt.Errorf("%s exists and is not outfit's: left as it is", rel)
	}
	if text == "" {
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := mkdirAllLike(in.W.WorkTree, rel); err != nil {
		return err
	}
	// Written under a temporary name in outfit's own directory and renamed into place: a kill in
	// between leaves nothing in the worktree (a temporary file beside it would show in `git status`).
	dir := filepath.Join(in.W.GitDir, "outfit", "tmp")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "ignore-"+hex.EncodeToString(sha256Sum(rel)))
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	fault.Point("the ignore file is written under a temporary name, not yet renamed")
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, syscall.EXDEV) { // the git dir is on another filesystem: the temporary file must sit beside the target
			return state.WriteFileAtomic(abs, []byte(text), 0o644)
		}
		return err
	}
	return nil
}

func sha256Sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:8]
}

// pruneEmptyDirs removes the directories a removed link left empty, from its own directory up to
// (not including) under. An empty directory holds nothing of anybody's, and git does not track it.
func pruneEmptyDirs(worktree, rel, under string) {
	for d := pathpkg.Dir(rel); d != "." && d != "/" && d != under && strings.HasPrefix(d, under+"/"); d = pathpkg.Dir(d) {
		if err := os.Remove(filepath.Join(worktree, filepath.FromSlash(d))); err != nil {
			return
		}
	}
}

// teardownExtras takes away the links of the profile's own files and the ignore files that listed
// them (uninstall): the record, plus every link of outfit's found below the directories. A regular
// file where a link was stays (it is somebody's bytes).
func teardownExtras(ctx context.Context, g gitx.Client, w Workspace, st *state.State) (did, failed []string) {
	dirs := extraScanDirs(w, st, nil)
	recorded := map[string]plan.Record{}
	for _, s := range st.Extras {
		if plan.ValidSwapPath(s.Path) == nil {
			recorded[s.Path] = plan.Record{Target: s.Target, Extra: true}
		}
	}
	in := Input{W: w}
	in.orphanExtras(dirs, recorded)
	var paths []string
	for p := range recorded {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// One question to git for all of them: a path it tracks is not ours to remove here.
	tracked := map[string]bool{}
	if len(paths) > 0 {
		args := append([]string{"--literal-pathspecs", "ls-files", "-z", "--"}, paths...)
		if res, err := g.Read(ctx, args...); err == nil {
			for _, p := range strings.Split(string(res.Stdout), "\x00") {
				tracked[p] = true
			}
		}
	}
	personal := personalLinkDirs(w)
	removed := 0
	for _, p := range paths {
		if tracked[p] || !realParents(w.WorkTree, p) {
			continue
		}
		abs := filepath.Join(w.WorkTree, filepath.FromSlash(p))
		fi, err := os.Lstat(abs)
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if t, _ := os.Readlink(abs); t != recorded[p].Target && !isExtraLink(p, t, personal) {
			failed = append(failed, p+" is a link, but not the one outfit made: left alone")
			continue
		}
		fault.Point("teardown: before an extra link goes")
		if err := os.Remove(abs); err != nil {
			failed = append(failed, "link "+p+": "+err.Error())
			continue
		}
		removed++
		if d := underDirOf(p, dirs); d != "" {
			pruneEmptyDirs(w.WorkTree, p, d)
		}
	}
	if removed > 0 {
		did = append(did, fmt.Sprintf("%d link(s) of the profile's own files", removed))
	}
	for _, d := range dirs {
		rel := d + "/.gitignore"
		if len(failed) > 0 {
			// A link is still there: the file that keeps git from seeing it stays too.
			break
		}
		if !realParents(w.WorkTree, rel) {
			continue
		}
		abs := filepath.Join(w.WorkTree, filepath.FromSlash(rel))
		if b, err := os.ReadFile(abs); err == nil && plan.OurIgnore(string(b)) {
			if err := os.Remove(abs); err != nil {
				failed = append(failed, rel+": "+err.Error())
			}
		}
	}
	return did, failed
}
