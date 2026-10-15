package apply

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLinkBesideFallback covers the link a git dir on another filesystem needs: the
// temporary name sits next to the target (docs/testing.md; the kill tests cannot reach it,
// they run on one filesystem).
func TestLinkBesideFallback(t *testing.T) {
	dir := t.TempDir()
	in := Input{W: Workspace{WorkTree: dir, GitDir: filepath.Join(dir, ".git")}}
	abs := filepath.Join(dir, "sub", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("base"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(abs); err != nil {
		t.Fatal(err)
	}
	if err := in.linkBeside(abs, "sub/AGENTS.md", "../.outfit/profiles/p/sub/AGENTS.md"); err != nil {
		t.Fatal(err)
	}
	if tgt, err := os.Readlink(abs); err != nil || tgt != "../.outfit/profiles/p/sub/AGENTS.md" {
		t.Fatalf("%q %v", tgt, err)
	}
	if _, err := os.Lstat(in.abs(tmpLinkPath("sub/AGENTS.md"))); err == nil {
		t.Fatal("the temporary link is still there")
	}

	// A leftover link of ours in the way is replaced; a person's own file of that name is not.
	tmp := in.abs(tmpLinkPath("sub/AGENTS.md"))
	if err := os.Symlink("stale", tmp); err != nil {
		t.Fatal(err)
	}
	if err := in.linkBeside(abs, "sub/AGENTS.md", "x"); err != nil {
		t.Fatalf("a stale temporary link blocked the link: %v", err)
	}
	if err := os.WriteFile(tmp, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := in.linkBeside(abs, "sub/AGENTS.md", "y")
	if err == nil || !strings.Contains(err.Error(), "not outfit's to remove") {
		t.Fatalf("a person's file was in the way: %v", err)
	}
	if b, _ := os.ReadFile(tmp); string(b) != "mine" {
		t.Fatal("the person's file was touched")
	}
}
