package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/fault"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// Teardown undoes sync in ONE worktree, in the order that matters (HANDOVER.md §4):
// links are removed and the base files restored BEFORE sparse checkout is disabled,
// because the other way round leaves ` T` in `git status` and lets `commit -a`
// commit the symlink. Only what outfit made is touched: a regular file where a link
// should be is somebody's bytes and stays. It returns what it did, for the report.
func Teardown(ctx context.Context, w Workspace) (did []string, failed []string) {
	st, err := state.Read(w.GitDir)
	if errors.Is(err, state.ErrNone) {
		return nil, nil
	}
	if err != nil {
		return nil, []string{"state file of " + w.WorkTree + ": " + err.Error()}
	}
	// No sync may run while the links come off: it would link again behind this, or find
	// its temporary link gone.
	lock, lerr := AcquireLock(w.GitDir)
	if lerr != nil {
		return nil, []string{"a sync is running in " + w.WorkTree + " (" + lerr.Error() + ")"}
	}
	defer lock.Release()
	g := gitx.Client{Dir: w.WorkTree}
	if w.WorkTree == "" {
		// Never "a deleted checkout": an unknown path means links may still stand
		// somewhere outfit cannot see, and the record must stay for the rerun.
		return nil, []string{"the path of the worktree with git dir " + w.GitDir + " is unknown (a separate git dir whose config has no core.worktree)"}
	}
	if !dirExists(w.WorkTree) {
		// The checkout was deleted by hand (not yet pruned): its links went with it and
		// there is no index to restore. Only the settings in its git dir are left.
		if st.SparseSet {
			g = gitx.Client{Dir: w.GitDir}
			cfgFile := filepath.Join(w.GitDir, "config.worktree")
			for _, k := range []string{"core.sparseCheckout", "core.sparseCheckoutCone", "index.sparse", "sparse.expectFilesOutsideOfPatterns"} {
				_, _ = g.Write(ctx, "config", "--file", cfgFile, "--unset-all", k)
			}
		}
		return nil, nil
	}

	// A directory the profile replaced holds the team's files only in git; a regular file the person
	// (or an agent) wrote below it while it was hidden is theirs, and sparse checkout coming off would
	// leave it half-present ("already present, not updated"). Stop and say where.
	// Only a directory the patterns hide NOW: once an earlier, killed uninstall has switched sparse
	// checkout off, the team's files are back and are the team's, not strays.
	replSparse, _ := readInside(w.GitDir, "info/sparse-checkout")
	var replaced []string
	if parsed, ok := plan.ParseSparse(string(replSparse)); ok && len(parsed.HideDirs) > 0 {
		// (the patterns file outlives `sparse-checkout disable` by a moment: ask git whether it is on)
		if res, err := g.Read(ctx, "config", "--get", "core.sparseCheckout"); err == nil && strings.TrimSpace(string(res.Stdout)) == "true" {
			replaced = append(replaced, parsed.HideDirs...)
		}
	}
	for _, d := range replaced {
		if plan.ValidSwapPath(d) != nil || !realParents(w.WorkTree, d+"/x") {
			continue
		}
		var own []string
		for _, f := range strayFilesIn(w.WorkTree, d) {
			if !isRecordedSwap(st, f) && !isOurIgnoreFile(w.WorkTree, f) { // an editor's save over a swapped path is handled with the links; our ignore file goes with them
				own = append(own, f)
			}
		}
		if strays := onlyTracked(ctx, g, d, own); len(strays) > 0 {
			failed = append(failed, fmt.Sprintf("%s, which the profile replaced, holds regular files at paths the team tracks that outfit did not make (%s): left alone (git would leave them where they are when the team's come back); move or commit them, then rerun", d, strings.Join(firstFew(strays, 3), ", ")))
		}
	}

	if len(failed) > 0 {
		return nil, failed // nothing is touched: links and patterns stay as they are
	}

	var restore []string
	swaps := append([]state.Swap(nil), st.Swaps...)
	swaps = append(swaps, unrecordedLinks(ctx, g, w, st)...)
	for _, s := range swaps {
		// state.json is local, but a removal it names must stay inside the worktree
		// and take away only OUR link: never a path through a symlinked directory, and
		// never a link that is not the one sync made.
		if err := plan.ValidSwapPath(s.Path); err != nil || !realParents(w.WorkTree, s.Path) {
			failed = append(failed, "recorded path "+s.Path+" is not a plain path inside the worktree: left alone")
			continue
		}
		removeStrays(ctx, g, w.WorkTree, s.Path, s.Target)
		p := filepath.Join(w.WorkTree, filepath.FromSlash(s.Path))
		fi, err := os.Lstat(p)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			if t, _ := os.Readlink(p); s.Target != "" && t != s.Target {
				failed = append(failed, s.Path+" is a link, but not the one outfit made: left alone")
				continue
			}
			fault.Point("teardown: before a link goes")
			if err := os.Remove(p); err != nil {
				failed = append(failed, "link "+s.Path+": "+err.Error())
				continue
			}
			did = append(did, "link "+s.Path)
			restore = append(restore, s.Path)
		case err == nil && cleanBaseFile(ctx, g, s.Path):
			// The base file is back, untouched: an earlier run (killed after it switched
			// sparse checkout off) already did this one. Nothing to remove, nothing to lose.
			restore = append(restore, s.Path)
		case err == nil:
			failed = append(failed, s.Path+" is a regular file where outfit's link was: left alone (its bytes are yours; `outfit sync` absorbs an editor's save into the profile, or saves both copies when the profile changed too, then rerun)")
		default:
			restore = append(restore, s.Path)
		}
	}

	// The profile's own files below the replaced directories, and the ignore files that listed them.
	exDid, exFailed := teardownExtras(ctx, g, w, st)
	did = append(did, exDid...)
	failed = append(failed, exFailed...)

	// A link that could not be removed (or a regular file where one was) stops here:
	// sparse checkout stays on, because switching it off while a link still stands at a
	// tracked path shows ` T` in status and lets `commit -a` commit the symlink. The
	// rerun, which still has the record, continues from this point.
	if len(failed) > 0 {
		return did, failed
	}

	if root, err := os.OpenRoot(filepath.Join(w.GitDir, "outfit")); err == nil {
		_ = root.RemoveAll("tmp") // links a killed sync left under their temporary names
		_ = root.Close()
	}
	fault.Point("teardown: links are off")
	sparseText, _ := readInside(w.GitDir, "info/sparse-checkout")
	managed := strings.HasPrefix(string(sparseText), plan.Marker)
	if managed || st.SparseSet {
		if managed {
			if _, err := g.Write(ctx, "sparse-checkout", "disable"); err != nil {
				failed = append(failed, "git sparse-checkout disable: "+err.Error())
			} else {
				fault.Point("teardown: sparse checkout is off")
				did = append(did, "sparse checkout")
			}
			_ = os.Remove(filepath.Join(w.GitDir, "info", "sparse-checkout"))
		}
		cfgFile := filepath.Join(w.GitDir, "config.worktree")
		keys := []string{"core.sparseCheckout", "core.sparseCheckoutCone"}
		// `git worktree add` copies the main worktree's config.worktree, this key
		// included, so a linked worktree never recorded setting it: with outfit's own
		// sparse patterns in place there it is outfit's to take away.
		if st.ExpectFilesSet || (managed && w.GitDir != w.CommonDir) {
			keys = append(keys, "sparse.expectFilesOutsideOfPatterns")
		}
		for _, k := range keys {
			// Exit 5: not set, which is fine.
			_, _ = g.Write(ctx, "config", "--file", cfgFile, "--unset-all", k)
		}
		// `git sparse-checkout disable` leaves index.sparse=false behind; a true
		// value is somebody's sparse index and stays.
		if res, err := g.Read(ctx, "config", "--file", cfgFile, "--get", "index.sparse"); err == nil && strings.TrimSpace(string(res.Stdout)) == "false" {
			_, _ = g.Write(ctx, "config", "--file", cfgFile, "--unset-all", "index.sparse")
		}
	}

	// Belt and braces: a swapped path git still marks skip-worktree (someone turned
	// sparse checkout off by hand first) would stay hidden for good.
	if len(restore) > 0 {
		args := append([]string{"--literal-pathspecs", "ls-files", "-t", "-z", "--"}, restore...)
		if res, err := g.Read(ctx, args...); err == nil {
			var still []string
			for _, rec := range strings.Split(string(res.Stdout), "\x00") {
				if tag, path, ok := strings.Cut(rec, " "); ok && tag == "S" {
					still = append(still, path)
				}
			}
			if len(still) > 0 {
				if _, err := g.Write(ctx, append([]string{"--literal-pathspecs", "update-index", "--no-skip-worktree", "--"}, still...)...); err != nil {
					failed = append(failed, "update-index --no-skip-worktree: "+err.Error())
				} else if _, err := g.Write(ctx, append([]string{"--literal-pathspecs", "checkout", "--"}, still...)...); err != nil {
					failed = append(failed, "git checkout of the base files: "+err.Error())
				} else {
					did = append(did, fmt.Sprintf("%d base file(s) restored", len(still)))
				}
			}
		}
	}
	return did, failed
}

// profileLinkText is the text of a committed profile's link: relative, up to the root,
// then `.outfit/profiles/`.
var profileLinkText = regexp.MustCompile(`^(\.\./)*\.outfit/profiles/`)

// isOurLinkText reports that a link's text points into one of outfit's profile
// directories: a committed profile (relative text), or a personal one (absolute). The
// absolute text is the spelling of the directory the sync ran from (a logical, maybe
// symlinked path), the uninstall may run from another: compare resolved paths too.
func isOurLinkText(t string, personal []string) bool {
	if profileLinkText.MatchString(t) {
		return true
	}
	if !filepath.IsAbs(t) {
		return false
	}
	resolved := t
	if dir, err := filepath.EvalSymlinks(filepath.Dir(t)); err == nil {
		resolved = filepath.Join(dir, filepath.Base(t))
	}
	for _, p := range personal {
		if strings.HasPrefix(t, p) || strings.HasPrefix(resolved, p) {
			return true
		}
		if rp, err := filepath.EvalSymlinks(strings.TrimSuffix(p, string(filepath.Separator))); err == nil && strings.HasPrefix(resolved, rp+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// unrecordedLinks finds links of ours that state.json does not list, and removes the
// leftover temporary links beside paths that are still hidden: a sync killed between making
// a link and writing the record leaves them (the skip-worktree bit is set, the file on disk
// is a symlink into a profile directory). Without this the uninstall that follows would
// switch sparse checkout off over them (` T`, `commit -a` commits a symlink).
func unrecordedLinks(ctx context.Context, g gitx.Client, w Workspace, st *state.State) []state.Swap {
	known := map[string]bool{}
	for _, s := range st.Swaps {
		known[s.Path] = true
	}
	res, err := g.Read(ctx, "ls-files", "-t", "-z")
	if err != nil {
		return nil
	}
	personal := []string{
		filepath.Join(w.CommonDir, "outfit", "profiles") + string(filepath.Separator),
		filepath.Join(w.GitDir, "outfit", "worktree-profiles") + string(filepath.Separator),
	}
	var out []state.Swap
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		tag, path, ok := strings.Cut(rec, " ")
		if !ok || tag != "S" || plan.ValidSwapPath(path) != nil || !realParents(w.WorkTree, path) {
			continue
		}
		// A temporary link beside the path (the fallback for a git dir on another
		// filesystem), ours by its text, not tracked.
		for _, stray := range []string{path + "~cruft", tmpLinkPath(path)} {
			sp := filepath.Join(w.WorkTree, filepath.FromSlash(stray))
			if t, err := os.Readlink(sp); err == nil && isOurLinkText(t, personal) {
				if res, err := g.Read(ctx, "--literal-pathspecs", "ls-files", "-z", "--", stray); err == nil && len(res.Stdout) == 0 {
					_ = os.Remove(sp)
				}
			}
		}
		if known[path] {
			continue
		}
		t, err := os.Readlink(filepath.Join(w.WorkTree, filepath.FromSlash(path)))
		if err != nil {
			continue
		}
		if isOurLinkText(t, personal) {
			out = append(out, state.Swap{Path: path, Target: t})
		}
	}
	return out
}

// removeStrays takes away OUR leftover links next to a swapped path: the temporary name a
// link is made under when the git dir is on another filesystem (a kill between creating it
// and renaming it leaves it; normally it is in the git dir, see linkVia) and
// `<path>~cruft` (older git, mid-merge). Only a symlink with the text outfit wrote, and
// only one git does not track: nothing of the repository's own.
func removeStrays(ctx context.Context, g gitx.Client, worktree, rel, target string) {
	if target == "" {
		return
	}
	for _, stray := range []string{rel + "~cruft", tmpLinkPath(rel)} {
		sp := filepath.Join(worktree, filepath.FromSlash(stray))
		t, err := os.Readlink(sp)
		if err != nil || t != target {
			continue
		}
		res, err := g.Read(ctx, "--literal-pathspecs", "ls-files", "-s", "-t", "-z", "--", stray)
		if err != nil {
			continue
		}
		if len(res.Stdout) > 0 {
			// Staged by `git add -A` (and not in HEAD): ours too, and a commit would put the
			// link into history. Anything else git tracks is the repository's own.
			if !stagedOnlyLink(ctx, g, stray, parseLsFiles(res.Stdout)[stray]) {
				continue
			}
			_, _ = g.Write(ctx, "--literal-pathspecs", "update-index", "--force-remove", "--", stray)
		}
		_ = os.Remove(sp)
	}
}

// cleanBaseFile reports that path is the committed base file, back on disk and not
// hidden by sparse checkout, and unchanged against the index.
func cleanBaseFile(ctx context.Context, g gitx.Client, rel string) bool {
	res, err := g.Read(ctx, "--literal-pathspecs", "ls-files", "-t", "-z", "--", rel)
	if err != nil || !strings.HasPrefix(string(res.Stdout), "H ") {
		return false
	}
	st, err := g.Read(ctx, "--literal-pathspecs", "status", "--porcelain=v1", "-z", "--untracked-files=no", "--", rel)
	return err == nil && len(st.Stdout) == 0
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func containsStr(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func firstFew(s []string, n int) []string {
	if len(s) > n {
		return append(append([]string(nil), s[:n]...), "…")
	}
	return s
}

func isRecordedSwap(st *state.State, path string) bool {
	for _, s := range st.Swaps {
		if s.Path == path {
			return true
		}
	}
	return false
}
