package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreesFindsTheMainWorktreeOfASubmoduleOrSeparateGitDir(t *testing.T) {
	root := t.TempDir()
	common := filepath.Join(root, "modules", "sub")
	if err := os.MkdirAll(common, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[core]\n\tbare = false\n\tworktree = ../../../sub\n[remote \"origin\"]\n\turl = x\n"
	if err := os.WriteFile(filepath.Join(common, "config"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Worktrees(common)
	want := filepath.Clean(filepath.Join(common, "../../../sub"))
	if len(got) != 1 || got[0].Path != want || !got[0].Main {
		t.Fatalf("%+v want path %s", got, want)
	}
	// A repository that names no worktree and is not called .git has no known path.
	other := filepath.Join(root, "bare-ish")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Worktrees(other); len(got) != 1 || got[0].Path != "" {
		t.Fatalf("%+v", got)
	}
}
