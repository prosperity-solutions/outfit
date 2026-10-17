package apply

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/profiles"
)

// A file system that cannot hard-link (exFAT, FAT) gets a symlink, and outfit remembers it: otherwise the
// upgrade of a symlink to a hard link would be planned again on every sync, for ever.
func TestAFileSystemWithoutHardLinksFallsBackOnceAndIsRemembered(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store", "mine")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	variant := filepath.Join(store, "AGENTS.md")
	if err := os.WriteFile(variant, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{W: Workspace{WorkTree: filepath.Join(dir, "wt"), GitDir: filepath.Join(dir, "gitdir")}, Profile: &profiles.Profile{Name: "mine", Tier: profiles.Local, Dir: store}}
	for _, d := range []string{in.W.WorkTree, filepath.Join(in.W.GitDir, "outfit")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	defer func(f func(string, string) error) { osLink = f }(osLink)
	osLink = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.ENOTSUP} }

	if !in.hardAllowed() {
		t.Fatal("hard links are allowed until one fails")
	}
	if err := in.link("AGENTS.md", variant, false, true); err != nil {
		t.Fatal(err)
	}
	if tgt, err := os.Readlink(filepath.Join(in.W.WorkTree, "AGENTS.md")); err != nil || tgt != variant {
		t.Fatalf("the symlink fallback: %q %v", tgt, err)
	}
	if in.hardAllowed() {
		t.Fatal("a file system that cannot hard-link must be remembered")
	}
	// ... but "too many links" and "another disk" are not about the file system: nothing is remembered.
	_ = os.Remove(in.noHardMarker())
	osLink = func(string, string) error { return &os.LinkError{Op: "link", Err: syscall.EMLINK} }
	if err := in.link("AGENTS.md", variant, false, true); err != nil {
		t.Fatal(err)
	}
	if !in.hardAllowed() {
		t.Fatal("EMLINK says nothing about the file system")
	}
	// An error that is not about hard links at all is an error.
	osLink = func(string, string) error { return &os.LinkError{Op: "link", Err: errors.New("disk on fire")} }
	if err := in.link("AGENTS.md", variant, false, true); err == nil {
		t.Fatal("an unrelated failure must not be hidden by a symlink")
	}
}
