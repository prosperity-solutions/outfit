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
	f := testkit.New(t, testkit.Team, testkit.WithWorktrees(2))
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
	f := testkit.New(t, testkit.Team, testkit.WithWorktrees(1))
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

// Review finding: inside a hook in a LINKED worktree git exports an absolute
// GIT_DIR under <common>/worktrees/<n>, and a hook may have `cd`'d into a
// package. The worktree root must come from the git dir's back-pointer, never
// from the cwd, or the root .outfit/ (and its `locked` keys) would be skipped
// and a nested pkg/.outfit/config would be read instead.
func TestHookInLinkedWorktreeFromSubdirectory(t *testing.T) {
	f := testkit.New(t, testkit.Team, testkit.WithWorktrees(1))
	wt := f.Worktrees[0]
	gitDir := f.GitIn(wt, "rev-parse", "--path-format=absolute", "--git-dir")
	sub := filepath.Join(wt, "apps", "web")

	got, err := repo.Discover(sub, func(k string) string {
		if k == "GIT_DIR" {
			return gitDir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval(t, got.WorkTree) != eval(t, wt) {
		t.Fatalf("WorkTree = %q, want %q", got.WorkTree, wt)
	}
}

func TestHookInMainWorktreeWithRelativeGitDirFromSubdirectory(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	sub := filepath.Join(f.Dir, "apps", "web", "src")
	got, err := repo.Discover(sub, func(k string) string {
		if k == "GIT_DIR" {
			return ".git" // exported relative to the worktree root, hook has since cd'd
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if eval(t, got.WorkTree) != eval(t, f.Dir) || eval(t, got.GitDir) != eval(t, filepath.Join(f.Dir, ".git")) {
		t.Fatalf("got %+v", got)
	}
}

// git >= 2.48 can store worktree back-pointers as relative paths.
func TestRelativeBackPointer(t *testing.T) {
	f := testkit.New(t, testkit.Team, testkit.WithWorktrees(1))
	wt := f.Worktrees[0]
	gitDir := f.GitIn(wt, "rev-parse", "--path-format=absolute", "--git-dir")
	// Rewrite the back-pointer the way useRelativePaths does.
	rel, err := filepath.Rel(gitDir, filepath.Join(eval(t, wt), ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "gitdir"), []byte(rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(wt, "apps", "web")
	got, err := repo.Discover(inner, func(k string) string {
		if k == "GIT_DIR" {
			return gitDir
		}
		return ""
	})
	if err != nil || eval(t, got.WorkTree) != eval(t, wt) {
		t.Fatalf("got %+v, %v", got, err)
	}
}
