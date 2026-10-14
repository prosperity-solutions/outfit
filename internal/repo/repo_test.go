package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func noenv(string) string { return "" }

func mkdir(t *testing.T, p ...string) string {
	t.Helper()
	d := filepath.Join(p...)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func real(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestPlainRepoFromSubdirectory(t *testing.T) {
	root := real(t, t.TempDir())
	mkdir(t, root, ".git")
	sub := mkdir(t, root, "apps", "web", "src")

	r, err := Discover(sub, noenv)
	if err != nil {
		t.Fatal(err)
	}
	want := Repo{WorkTree: root, GitDir: filepath.Join(root, ".git"), CommonDir: filepath.Join(root, ".git")}
	if r != want {
		t.Fatalf("got %+v, want %+v", r, want)
	}
	if r.Linked() {
		t.Fatal("a plain repo is not a linked worktree")
	}
}

func TestLinkedWorktreeFollowsGitdirAndCommondir(t *testing.T) {
	root := real(t, t.TempDir())
	common := mkdir(t, root, "main", ".git")
	gd := mkdir(t, common, "worktrees", "wt1")
	write(t, filepath.Join(gd, "commondir"), "../..\n")
	wt := mkdir(t, root, "wt1")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+gd+"\n")

	r, err := Discover(mkdir(t, wt, "deep", "dir"), noenv)
	if err != nil {
		t.Fatal(err)
	}
	want := Repo{WorkTree: wt, GitDir: gd, CommonDir: common}
	if r != want {
		t.Fatalf("got %+v, want %+v", r, want)
	}
	if !r.Linked() {
		t.Fatal("want a linked worktree")
	}
}

func TestRelativeGitdirResolvesAgainstTheGitFile(t *testing.T) {
	root := real(t, t.TempDir())
	common := mkdir(t, root, "main", ".git")
	gd := mkdir(t, common, "worktrees", "wt1")
	write(t, filepath.Join(gd, "commondir"), "../..")
	wt := mkdir(t, root, "wt1")
	write(t, filepath.Join(wt, ".git"), "gitdir: ../main/.git/worktrees/wt1\n")

	r, err := Discover(wt, noenv)
	if err != nil || r.GitDir != gd || r.CommonDir != common {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestGitDirEnvWinsAndIsUsedInsideHooks(t *testing.T) {
	root := real(t, t.TempDir())
	common := mkdir(t, root, "main", ".git")
	gd := mkdir(t, common, "worktrees", "wt1")
	write(t, filepath.Join(gd, "commondir"), "../..")
	elsewhere := mkdir(t, root, "elsewhere")

	env := func(k string) string {
		if k == "GIT_DIR" {
			return gd
		}
		return ""
	}
	r, err := Discover(elsewhere, env)
	if err != nil || r.GitDir != gd || r.CommonDir != common {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestPlainGitDirEnvInfersWorkTree(t *testing.T) {
	root := real(t, t.TempDir())
	gd := mkdir(t, root, ".git")
	r, err := Discover(root, func(k string) string {
		if k == "GIT_DIR" {
			return ".git" // relative, as git sets it for the main worktree
		}
		return ""
	})
	if err != nil || r.GitDir != gd || r.WorkTree != root || r.CommonDir != gd {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestNotARepo(t *testing.T) {
	if _, err := Discover(real(t, t.TempDir()), noenv); err == nil {
		// The temp dir could sit inside a repo on a developer machine; only fail
		// when the error is not the expected one.
		t.Skip("temp dir is inside a git repository")
	} else if err != ErrNotRepo {
		t.Fatalf("got %v, want ErrNotRepo", err)
	}
}

func TestBrokenGitFile(t *testing.T) {
	root := real(t, t.TempDir())
	write(t, filepath.Join(root, ".git"), "garbage\n")
	if _, err := Discover(root, noenv); err == nil {
		t.Fatal("a .git file without `gitdir:` must be an error")
	}
	write(t, filepath.Join(root, ".git"), "gitdir:   \n")
	if _, err := Discover(root, noenv); err == nil {
		t.Fatal("an empty gitdir must be an error")
	}
}
