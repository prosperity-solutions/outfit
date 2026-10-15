package apply

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	g := gitx.Client{Dir: w.WorkTree}
	if w.WorkTree == "" || !dirExists(w.WorkTree) {
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

	var restore []string
	for _, s := range st.Swaps {
		p := filepath.Join(w.WorkTree, filepath.FromSlash(s.Path))
		fi, err := os.Lstat(p)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink != 0:
			if err := os.Remove(p); err != nil {
				failed = append(failed, "link "+s.Path+": "+err.Error())
				continue
			}
			did = append(did, "link "+s.Path)
			restore = append(restore, s.Path)
		case err == nil:
			failed = append(failed, s.Path+" is a regular file where outfit's link was: left alone (its bytes are yours)")
		default:
			restore = append(restore, s.Path)
		}
	}

	sparseText, _ := os.ReadFile(filepath.Join(w.GitDir, "info", "sparse-checkout"))
	managed := strings.HasPrefix(string(sparseText), plan.Marker)
	if managed || st.SparseSet {
		if managed {
			if _, err := g.Write(ctx, "sparse-checkout", "disable"); err != nil {
				failed = append(failed, "git sparse-checkout disable: "+err.Error())
			} else {
				did = append(did, "sparse checkout")
			}
			_ = os.Remove(filepath.Join(w.GitDir, "info", "sparse-checkout"))
		}
		cfgFile := filepath.Join(w.GitDir, "config.worktree")
		keys := []string{"core.sparseCheckout", "core.sparseCheckoutCone"}
		if st.ExpectFilesSet {
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

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
