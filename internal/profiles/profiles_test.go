package profiles_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/profiles"
	"github.com/prosperity-solutions/outfit/internal/repo"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

func TestValidName(t *testing.T) {
	for _, ok := range []string{"yolo", "lean", "my-setup", "a.b_c", "x1"} {
		if err := profiles.ValidName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "base", "Yolo", "-x", ".hidden", "a/b", "../x", "a..b", "a b", "x\x00"} {
		if profiles.ValidName(bad) == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func discover(t *testing.T, f *testkit.Repo) repo.Repo {
	t.Helper()
	r, err := repo.Discover(f.Dir, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestCommittedProfilesFromDiskAndHead(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	r := discover(t, f)
	g := gitx.Client{Dir: f.Dir, Env: f.Env}

	l := profiles.List(context.Background(), g, r)
	var names []string
	for _, p := range l.Profiles {
		names = append(names, p.Name+":"+string(p.Tier))
	}
	if !reflect.DeepEqual(names, []string{"lean:committed", "yolo:committed"}) {
		t.Fatalf("got %v", names)
	}

	// Hidden from disk but still in HEAD: still listed, files and config readable.
	if err := os.RemoveAll(filepath.Join(f.Dir, ".outfit", "profiles", "yolo")); err != nil {
		t.Fatal(err)
	}
	l = profiles.List(context.Background(), g, r)
	if len(l.Profiles) != 2 {
		t.Fatalf("a profile hidden from disk must still be listed: %+v", l.Profiles)
	}
	if got := profiles.HeadFiles(context.Background(), g, "yolo"); !reflect.DeepEqual(got, []string{"AGENTS.md", "CLAUDE.md"}) {
		t.Fatalf("head files = %v", got)
	}
	if cfg := profiles.HeadConfig(context.Background(), g, "yolo"); cfg.Keys["biome"] != "off" || cfg.Keys["hook.pre-commit"] != "off" {
		t.Fatalf("head config = %+v", cfg.Keys)
	}
	if _, ok := profiles.Locate(r, "yolo"); ok {
		t.Fatal("Locate looks at disk only: a hidden profile is not found")
	}
	if _, ok := profiles.Locate(r, "lean"); !ok {
		t.Fatal("lean is on disk")
	}
}

func TestLocalAndWorktreeTiersAndNameClash(t *testing.T) {
	f := testkit.New(t, testkit.Team, testkit.WithWorktrees(1))
	r := discover(t, f)
	g := gitx.Client{Dir: f.Dir, Env: f.Env}

	mk := func(dir string) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(profiles.TierDir(r, profiles.Local), "mine"))
	mk(filepath.Join(profiles.TierDir(r, profiles.Local), "yolo")) // clashes with a committed profile
	mk(filepath.Join(profiles.TierDir(r, profiles.Worktree), "scratch"))

	l := profiles.List(context.Background(), g, r)
	got := map[string]profiles.Tier{}
	for _, p := range l.Profiles {
		got[p.Name] = p.Tier
	}
	want := map[string]profiles.Tier{"lean": profiles.Committed, "yolo": profiles.Committed, "mine": profiles.Local, "scratch": profiles.Worktree}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0].Message, "outfit new <other-name> --from local:yolo") {
		t.Fatalf("the clash must name the fix: %v", l.Warnings)
	}

	// The worktree tier lives in the per-worktree git dir, not the common one.
	wt := f.Worktrees[0]
	rw, err := repo.Discover(wt, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if profiles.TierDir(rw, profiles.Worktree) == profiles.TierDir(r, profiles.Worktree) {
		t.Fatal("each worktree has its own worktree tier")
	}
	real := func(p string) string {
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if real(profiles.TierDir(rw, profiles.Local)) != real(profiles.TierDir(r, profiles.Local)) {
		t.Fatal("the local tier is shared by all worktrees")
	}
}

func TestLocateRejectsUnsafeNames(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	r := discover(t, f)
	for _, n := range []string{"../../etc", "base", "", "a/b"} {
		if _, ok := profiles.Locate(r, n); ok {
			t.Errorf("%q must not resolve", n)
		}
	}
}

func TestFilesExcludeProfileConfig(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	r := discover(t, f)
	p, ok := profiles.Locate(r, "lean")
	if !ok {
		t.Fatal("lean")
	}
	if got := profiles.Files(p); !reflect.DeepEqual(got, []string{"apps/web/AGENTS.md"}) {
		t.Fatalf("files = %v", got)
	}
	cfg, err := profiles.ReadConfig(p)
	if err != nil || cfg.Keys["biome"] != "off" {
		t.Fatalf("cfg = %+v, %v", cfg, err)
	}
}

// Review finding: a committed symlink must never make outfit read or list files
// outside the repository.
func TestCommittedSymlinksAreNeverFollowed(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	r := discover(t, f)
	g := gitx.Client{Dir: f.Dir, Env: f.Env}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, profiles.ProfileConfig), []byte("[outfit]\n leaked = yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A profile directory that is a symlink.
	prof := filepath.Join(f.Dir, ".outfit", "profiles", "evil")
	if err := os.Symlink(outside, prof); err != nil {
		t.Fatal(err)
	}
	if _, ok := profiles.Locate(r, "evil"); ok {
		t.Fatal("a symlinked profile dir must not be located")
	}
	for _, p := range profiles.List(context.Background(), g, r).Profiles {
		if p.Name == "evil" {
			t.Fatal("a symlinked profile dir must not be listed")
		}
	}

	// A symlink INSIDE a real profile is not a swap source.
	lean, _ := profiles.Locate(r, "lean")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(lean.Dir, "LINK.md")); err != nil {
		t.Fatal(err)
	}
	for _, name := range profiles.Files(lean) {
		if name == "LINK.md" {
			t.Fatal("symlinks must not be listed as profile files")
		}
	}

	// The whole .outfit/profiles directory replaced by a symlink.
	if err := os.RemoveAll(filepath.Join(f.Dir, ".outfit", "profiles")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(f.Dir, ".outfit", "profiles")); err != nil {
		t.Fatal(err)
	}
	if _, ok := profiles.Locate(r, "lean"); ok {
		t.Fatal("nothing under a symlinked .outfit/profiles may be located")
	}
	l := profiles.List(context.Background(), g, r)
	for _, p := range l.Profiles {
		if strings.Contains(p.Dir, "secret") || p.Name == "secret.txt" {
			t.Fatalf("listed outside content: %+v", p)
		}
	}
	if len(l.Warnings) == 0 {
		t.Fatal("a refused symlink must be reported")
	}
}
