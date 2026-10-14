package hooks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// Guard decides whether a commit may proceed. With nothing swapped it returns
// immediately without spawning git, so `base` and non-swapping profiles pay
// nothing. Otherwise it refuses when:
//
//   - the expected sparse config is missing (then git would happily commit the
//     symlink: `git sparse-checkout disable` followed by `commit -a` did, verified);
//   - any swapped path is staged as mode 120000 (never allowed);
//   - any swapped path is staged at all, unless OUTFIT_ALLOW=1 or a merge is in
//     progress and a regular-file blob is staged (resolving a conflict on a swapped
//     path is legitimate).
func Guard(ctx context.Context, g gitx.Client, st *state.State, gitDir string, gc config.GitConfig, getenv func(string) string) ([]Violation, error) {
	if st == nil || len(st.Swaps) == 0 {
		return nil, nil
	}
	swapped := make(map[string]bool, len(st.Swaps))
	for _, s := range st.Swaps {
		swapped[s.Path] = true
	}

	var out []Violation
	if v := checkSparse(st, gitDir, gc); v != nil {
		out = append(out, *v)
	}

	res, err := g.Read(ctx, "diff", "--cached", "--raw", "-z", "--no-renames", "--no-ext-diff")
	if err != nil {
		return out, err
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

// checkSparse verifies the sparse-checkout config that keeps swapped paths out
// of `git add`: enabled, outfit-managed, and hiding every swapped path.
func checkSparse(st *state.State, gitDir string, gc config.GitConfig) *Violation {
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
	for _, s := range st.Swaps {
		if !lines["!/"+s.Path] {
			return &Violation{Path: s.Path, Message: fmt.Sprintf("the sparse-checkout file no longer hides %s", s.Path), Fix: fix}
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
