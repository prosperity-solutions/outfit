package repo_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/repo"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

func eval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Discovery without exec must agree with what git itself reports, for the main
// worktree and for linked worktrees.
func TestAgreesWithGit(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo, testkit.WithWorktrees(2))
	for _, dir := range append([]string{f.Dir}, f.Worktrees...) {
		sub := filepath.Join(dir, "apps", "web")
		got, err := repo.Discover(sub, func(string) string { return "" })
		if err != nil {
			t.Fatal(err)
		}
		gitDir := f.GitIn(sub, "rev-parse", "--path-format=absolute", "--git-dir")
		common := f.GitIn(sub, "rev-parse", "--path-format=absolute", "--git-common-dir")
		top := f.GitIn(sub, "rev-parse", "--show-toplevel")
		if eval(t, got.GitDir) != eval(t, gitDir) || eval(t, got.CommonDir) != eval(t, common) || eval(t, got.WorkTree) != eval(t, top) {
			t.Errorf("in %s: got %+v; git says gitdir=%s common=%s top=%s", dir, got, gitDir, common, top)
		}
	}
}

// Inside a hook git exports GIT_DIR; the repo must resolve from it alone.
func TestHookStyleGitDir(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo, testkit.WithWorktrees(1))
	wt := f.Worktrees[0]
	gitDir := f.GitIn(wt, "rev-parse", "--path-format=absolute", "--git-dir")
	got, err := repo.Discover(os.TempDir(), func(k string) string {
		if k == "GIT_DIR" {
			return gitDir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Linked() || eval(t, got.CommonDir) != eval(t, filepath.Join(f.Dir, ".git")) {
		t.Fatalf("got %+v", got)
	}
}
