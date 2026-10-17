package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// Violation is one reason the pre-commit guard refuses a commit. Every message
// carries the exact fix: agents follow error text.
type Violation struct {
	Path    string
	Message string
	Fix     string
}

func (v Violation) String() string {
	if v.Fix == "" {
		return v.Message
	}
	return v.Message + "\n  fix: " + v.Fix
}

// Guard decides whether a commit may proceed. The swapped set is what state.json
// records UNION what outfit's sparse-checkout file hides, so a gap between the
// two (a link made before the state was written, a state file lost) cannot turn
// the guard off. With nothing swapped it returns without spawning git, so `base`
// and non-swapping profiles pay nothing. Otherwise it refuses when:
//
//   - the expected sparse config is missing (then git would happily commit the
//     symlink: `git sparse-checkout disable` followed by `commit -a` did, verified);
//   - any swapped path, or the `<path>~cruft` git 2.36 moves our link to during a merge
//     conflict, is staged as mode 120000 (never allowed);
//   - any swapped path is staged at all, unless OUTFIT_ALLOW=1 or a merge is in
//     progress and a regular-file blob is staged (resolving a conflict on a swapped
//     path is legitimate).
//
// It fails CLOSED: an unreadable state file, an unreadable index, or a git config
// outfit could not read (configKnown=false) while paths are swapped refuses the
// commit with a fix, because the guard exists to keep links out of history.
func Guard(ctx context.Context, g gitx.Client, gitDir string, gc config.GitConfig, configKnown bool, getenv func(string) string) ([]Violation, error) {
	st, serr := state.Read(gitDir)
	if serr != nil && !errors.Is(serr, state.ErrNone) {
		return []Violation{{Message: "outfit's state file cannot be read, so swapped files cannot be protected: " + serr.Error(),
			Fix: "outfit init   (rewrites it; or commit with OUTFIT_ALLOW=1 on purpose)"}}, nil
	}
	sparseText, _ := os.ReadFile(filepath.Join(gitDir, "info", "sparse-checkout"))
	swapped := map[string]bool{}
	profileFile := map[string]string{} // swapped path -> the profile's file (absolute), for a local or worktree profile
	linked := map[string][2]uint64{}   // swapped path -> the disk and inode of the hard link outfit made there
	if st != nil {
		for _, s := range st.Swaps {
			swapped[s.Path] = true
			if filepath.IsAbs(s.Target) {
				profileFile[s.Path] = s.Target
				if s.Inode != 0 {
					linked[s.Path] = [2]uint64{s.Dev, s.Inode}
				}
			}
		}
	}
	for _, p := range hiddenPaths(string(sparseText)) {
		swapped[p] = true
	}
	// Directories the active profile replaces: recorded in state.json AND read back from the
	// patterns, so losing one of the two cannot turn the guard off.
	replaced := hiddenDirs(string(sparseText))
	if st != nil {
		for _, d := range st.ReplaceDirs {
			if !contains(replaced, d) {
				replaced = append(replaced, d)
			}
		}
	}
	sort.Strings(replaced)
	if len(swapped) == 0 && len(replaced) == 0 {
		return nil, nil
	}
	if !configKnown {
		return []Violation{{Message: "outfit could not read the git config, so it cannot tell whether swapped files are protected",
			Fix: "git config --list   (fix what it reports; or commit with OUTFIT_ALLOW=1 on purpose)"}}, nil
	}

	var out []Violation
	sparseViolation := checkSparse(swapped, replaced, gitDir, gc)

	res, err := g.Read(ctx, "diff", "--cached", "--raw", "--no-abbrev", "-z", "--no-renames", "--no-ext-diff")
	if err != nil {
		return append(out, Violation{Message: "outfit could not read the index to check swapped files: " + err.Error(), Fix: "git status"}), nil
	}
	allow := getenv("OUTFIT_ALLOW") == "1"
	merging := exists(filepath.Join(gitDir, "MERGE_HEAD"))
	for _, e := range parseRaw(res.Stdout) {
		// git 2.36 moves our link aside to `<path>~cruft` when a merge conflicts on a swapped
		// path, and `merge --abort` runs no hook to put things right: `git add -A` would stage
		// that symlink. (Found by the property test.)
		if base, ok := strings.CutSuffix(e.path, "~cruft"); ok && swapped[base] && e.newMode != "000000" {
			out = append(out, Violation{Path: e.path,
				Message: fmt.Sprintf("%s is outfit's own link for %s (a symlink, or a hard link of the profile's file), moved aside by git during a merge: committing it would put a link, or your profile's text, into history", e.path, base),
				Fix:     fmt.Sprintf("git reset -q -- %s && rm -f %s && outfit sync", shArg(e.path), shArg(e.path))})
			continue
		}
		if d, ok := under(e.path, replaced); ok && !swapped[e.path] {
			switch {
			case e.newMode == "120000":
				out = append(out, Violation{Path: e.path,
					Message: fmt.Sprintf("%s is staged as a symlink (mode 120000) below %s, which your active profile replaces: committing it would put outfit's link into history", e.path, d),
					Fix:     fmt.Sprintf("git reset -q -- %s && outfit sync", shArg(e.path))})
			case allow:
			case merging && (e.newMode == "100644" || e.newMode == "100755" || e.newMode == "000000") && !stagesProfileText(ctx, g, profileFile[e.path], linked[e.path], e):
			default:
				out = append(out, Violation{Path: e.path,
					Message: fmt.Sprintf("%s is below %s, which your active profile replaces: what is staged there is not what your checkout shows, and must not be committed from it", e.path, d),
					Fix:     fmt.Sprintf("git reset -q -- %s   (to commit it on purpose: OUTFIT_ALLOW=1 git commit …, or `outfit use base` first)", shArg(e.path))})
			}
			continue
		}
		if !swapped[e.path] {
			continue
		}
		switch {
		case e.newMode == "120000":
			out = append(out, Violation{Path: e.path,
				Message: fmt.Sprintf("%s is staged as a symlink (mode 120000): committing it would put outfit's link into history", e.path),
				Fix:     fmt.Sprintf("git reset -q -- %s && outfit sync", shArg(e.path))})
		case allow:
		case merging && (e.newMode == "100644" || e.newMode == "100755" || e.newMode == "000000") && !stagesProfileText(ctx, g, profileFile[e.path], linked[e.path], e):
		default:
			out = append(out, Violation{Path: e.path,
				Message: fmt.Sprintf("%s is swapped by your active profile and must not be committed from this checkout", e.path),
				Fix:     fmt.Sprintf("git reset -q -- %s   (to commit it on purpose: OUTFIT_ALLOW=1 git commit …)", shArg(e.path))})
		}
	}
	if sparseViolation != nil {
		// `outfit sync` refuses while a swapped path is staged as a symlink, so the one
		// command that works says both steps.
		for _, v := range out {
			if strings.Contains(v.Message, "staged as a symlink") {
				sparseViolation.Fix = "git reset -q && outfit sync"
				break
			}
		}
		out = append([]Violation{*sparseViolation}, out...)
	}
	return out, nil
}

// hiddenPaths lists the swapped paths an outfit-managed sparse-checkout file hides
// (`!/<path>` lines, read back through plan.ParseSparse so escaped names compare equal),
// excluding the inactive-profile pattern.
func hiddenPaths(text string) []string {
	o, ok := plan.ParseSparse(text)
	if !ok {
		return nil
	}
	var out []string
	for _, p := range o.Hidden {
		if !strings.HasPrefix(p, ".outfit/") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// hiddenDirs lists the directories an outfit-managed sparse-checkout file hides as a whole
// (`!/dir/` lines): the directories the active profile replaces.
func hiddenDirs(text string) []string {
	o, ok := plan.ParseSparse(text)
	if !ok {
		return nil
	}
	var out []string
	for _, d := range o.HideDirs {
		if d != ".outfit" && !strings.HasPrefix(d, ".outfit/") {
			out = append(out, d)
		}
	}
	sort.Strings(out)
	return out
}

// under reports whether path is inside one of the directories.
func under(path string, dirs []string) (string, bool) {
	for _, d := range dirs {
		if strings.HasPrefix(path, d+"/") {
			return d, true
		}
	}
	return "", false
}

// checkSparse verifies the sparse-checkout config that keeps swapped paths out
// of `git add`: enabled, outfit-managed, and hiding every swapped path.
func checkSparse(swapped map[string]bool, replaced []string, gitDir string, gc config.GitConfig) *Violation {
	fix := "outfit sync"
	if !gc.SparseCheckout {
		return &Violation{Message: "sparse checkout is off, so git no longer hides outfit's swapped files from `git add` and `commit -a`", Fix: fix}
	}
	b, err := os.ReadFile(filepath.Join(gitDir, "info", "sparse-checkout"))
	if err != nil {
		return &Violation{Message: "the sparse-checkout file outfit manages is missing", Fix: fix}
	}
	text := string(b)
	if !strings.HasPrefix(text, state.SparseMarker) {
		return &Violation{Message: "the sparse-checkout file is not outfit-managed (marker line missing)", Fix: fix}
	}
	parsed, ok := plan.ParseSparse(text)
	if !ok {
		return &Violation{Message: "the sparse-checkout file has lines outfit does not recognise", Fix: fix}
	}
	hiddenDirSet := map[string]bool{}
	for _, d := range parsed.HideDirs {
		hiddenDirSet[d] = true
	}
	hiddenSet := map[string]bool{}
	for _, p := range parsed.Hidden {
		hiddenSet[p] = true
	}
	paths := make([]string, 0, len(swapped))
	for p := range swapped {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, d := range replaced {
		if !hiddenDirSet[d] {
			return &Violation{Path: d, Message: fmt.Sprintf("the sparse-checkout file no longer hides %s, the directory your profile replaces", d), Fix: fix}
		}
	}
	for _, p := range paths {
		if _, in := under(p, replaced); in {
			continue // hidden with its directory
		}
		if !hiddenSet[p] {
			return &Violation{Path: p, Message: fmt.Sprintf("the sparse-checkout file no longer hides %s", p), Fix: fix}
		}
	}
	return nil
}

type rawEntry struct {
	oldMode, newMode string
	newBlob          string
	status           string
	path             string
}

// parseRaw parses `git diff --raw -z --no-renames`: ":<om> <nm> <os> <ns> <S>\0<path>\0".
func parseRaw(b []byte) []rawEntry {
	var out []rawEntry
	parts := bytes.Split(b, []byte{0})
	for i := 0; i+1 < len(parts); i += 2 {
		meta := strings.TrimPrefix(string(parts[i]), ":")
		f := strings.Fields(meta)
		if len(f) < 5 {
			continue
		}
		out = append(out, rawEntry{oldMode: f[0], newMode: f[1], newBlob: f[3], status: f[4], path: string(parts[i+1])})
	}
	return out
}

// stagesProfileText reports that the blob staged for e is the private text of a local or worktree profile:
// the profile's file now (profile is its absolute path, "" for a committed profile), or what this worktree's
// file still holds when it is the hard link outfit made (id is its disk and inode) although the profile has
// moved on to a new file since. Resolving a merge conflict legitimately stages the team's file, never that.
// The blob is made the way git makes it for the swapped path (--path: .gitattributes, filters).
func stagesProfileText(ctx context.Context, g gitx.Client, profile string, id [2]uint64, e rawEntry) bool {
	if profile == "" || e.newMode == "000000" {
		return false
	}
	blobOf := func(file string) bool {
		res, err := g.Read(ctx, "hash-object", "--path="+e.path, "--", file)
		return err == nil && strings.TrimSpace(string(res.Stdout)) == e.newBlob
	}
	if blobOf(profile) {
		return true
	}
	if id[1] == 0 {
		return false
	}
	top, err := g.Read(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		return false
	}
	file := filepath.Join(strings.TrimSpace(string(top.Stdout)), filepath.FromSlash(e.path))
	if fi, err := os.Lstat(file); err == nil && fi.Mode().IsRegular() {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && uint64(st.Dev) == id[0] && uint64(st.Ino) == id[1] { //nolint:unconvert // platform types
			return blobOf(file)
		}
	}
	return false
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// shArg quotes a path for display in a fix command.
func shArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`;&|<>()*?[]#~=%!{}") {
		return s
	}
	return shQuote(s)
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
