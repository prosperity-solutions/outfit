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

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
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
//   - any swapped path is staged as mode 120000 (never allowed);
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
	if st != nil {
		for _, s := range st.Swaps {
			swapped[s.Path] = true
		}
	}
	for _, p := range hiddenPaths(string(sparseText)) {
		swapped[p] = true
	}
	if len(swapped) == 0 {
		return nil, nil
	}
	if !configKnown {
		return []Violation{{Message: "outfit could not read the git config, so it cannot tell whether swapped files are protected",
			Fix: "git config --list   (fix what it reports; or commit with OUTFIT_ALLOW=1 on purpose)"}}, nil
	}

	var out []Violation
	if v := checkSparse(swapped, gitDir, gc); v != nil {
		out = append(out, *v)
	}

	res, err := g.Read(ctx, "diff", "--cached", "--raw", "-z", "--no-renames", "--no-ext-diff")
	if err != nil {
		return append(out, Violation{Message: "outfit could not read the index to check swapped files: " + err.Error(), Fix: "git status"}), nil
	}
	allow := getenv("OUTFIT_ALLOW") == "1"
	merging := exists(filepath.Join(gitDir, "MERGE_HEAD"))
	for _, e := range parseRaw(res.Stdout) {
		if !swapped[e.path] {
			continue
		}
		switch {
		case e.newMode == "120000":
			out = append(out, Violation{Path: e.path,
				Message: fmt.Sprintf("%s is staged as a symlink (mode 120000): committing it would put outfit's link into history", e.path),
				Fix:     fmt.Sprintf("git reset -q -- %s && outfit sync", shArg(e.path))})
		case allow:
		case merging && (e.newMode == "100644" || e.newMode == "100755"):
		default:
			out = append(out, Violation{Path: e.path,
				Message: fmt.Sprintf("%s is swapped by your active profile and must not be committed from this checkout", e.path),
				Fix:     fmt.Sprintf("git reset -q -- %s   (to commit it on purpose: OUTFIT_ALLOW=1 git commit …)", shArg(e.path))})
		}
	}
	return out, nil
}

// hiddenPaths lists the swapped paths an outfit-managed sparse-checkout file
// hides (`!/<path>` lines), excluding the inactive-profile pattern.
func hiddenPaths(text string) []string {
	if !strings.HasPrefix(text, state.SparseMarker) {
		return nil
	}
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if p, ok := strings.CutPrefix(l, "!/"); ok && p != "" && !strings.HasPrefix(p, ".outfit/") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// checkSparse verifies the sparse-checkout config that keeps swapped paths out
// of `git add`: enabled, outfit-managed, and hiding every swapped path.
func checkSparse(swapped map[string]bool, gitDir string, gc config.GitConfig) *Violation {
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
	lines := map[string]bool{}
	for _, l := range strings.Split(text, "\n") {
		lines[l] = true
	}
	paths := make([]string, 0, len(swapped))
	for p := range swapped {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if !lines["!/"+p] {
			return &Violation{Path: p, Message: fmt.Sprintf("the sparse-checkout file no longer hides %s", p), Fix: fix}
		}
	}
	return nil
}

type rawEntry struct {
	oldMode, newMode string
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
		out = append(out, rawEntry{oldMode: f[0], newMode: f[1], status: f[4], path: string(parts[i+1])})
	}
	return out
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

// shArg quotes a path for display in a fix command.
func shArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`;&|<>()*?[]#~=%!{}") {
		return s
	}
	return shQuote(s)
}
