package profiles

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/repo"
)

func fakeRepo(t *testing.T) repo.Repo {
	t.Helper()
	root := t.TempDir()
	r := repo.Repo{WorkTree: filepath.Join(root, "wt"), GitDir: filepath.Join(root, "wt", ".git"), CommonDir: filepath.Join(root, "wt", ".git")}
	if err := os.MkdirAll(r.GitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func list(r repo.Repo) Listing { return List(context.Background(), gitx.Client{Dir: r.WorkTree}, r) }

func TestCreateFromNothingHasATemplateConfig(t *testing.T) {
	r := fakeRepo(t)
	c, err := Create(context.Background(), gitx.Client{}, r, list(r), "mine", Local, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(c.Dir, ProfileConfig))
	if err != nil || !strings.Contains(string(b), "[outfit]") || c.Files != 1 {
		t.Fatalf("%q %v files=%d", b, err, c.Files)
	}
	if want := filepath.Join(r.CommonDir, "outfit", "profiles", "mine"); c.Dir != want {
		t.Fatalf("dir %s != %s", c.Dir, want)
	}
}

func TestCreateCopiesFilesKeepsTheExecBitAndSkipsLinks(t *testing.T) {
	r := fakeRepo(t)
	src := filepath.Join(r.CommonDir, "outfit", "profiles", "a")
	write(t, filepath.Join(src, ProfileConfig), "[outfit]\n\tbiome = off\n", 0o644)
	write(t, filepath.Join(src, "AGENTS.md"), "a\n", 0o644)
	write(t, filepath.Join(src, "apps/web/run.sh"), "#!/bin/sh\n", 0o755)
	if err := os.Symlink("/etc/passwd", filepath.Join(src, "link.md")); err != nil {
		t.Fatal(err)
	}
	l := list(r)
	a, err := Resolve(l, "a")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Create(context.Background(), gitx.Client{}, r, l, "b", Worktree, &a)
	if err != nil {
		t.Fatal(err)
	}
	if c.Files != 3 {
		t.Fatalf("files=%d", c.Files)
	}
	if want := filepath.Join(r.GitDir, "outfit", "worktree-profiles", "b"); c.Dir != want {
		t.Fatalf("a worktree profile lives in the git dir: %s != %s", c.Dir, want)
	}
	if _, err := os.Lstat(filepath.Join(c.Dir, "link.md")); err == nil {
		t.Fatal("a link was copied")
	}
	if fi, err := os.Stat(filepath.Join(c.Dir, "apps/web/run.sh")); err != nil || fi.Mode()&0o100 == 0 {
		t.Fatalf("exec bit lost: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(c.Dir, ProfileConfig)); !strings.Contains(string(b), "biome = off") {
		t.Fatalf("config not copied: %q", b)
	}
}

func TestCreateNeverMergesIntoOrShadowsAnExistingProfile(t *testing.T) {
	r := fakeRepo(t)
	write(t, filepath.Join(r.WorkTree, ".outfit/profiles/yolo/AGENTS.md"), "y\n", 0o644)
	if _, err := Create(context.Background(), gitx.Client{Dir: r.WorkTree}, r, list(r), "yolo", Local, nil); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("shadowing a committed profile: %v", err)
	}
	for _, bad := range []string{"base", "Bad", "../x", ""} {
		if _, err := Create(context.Background(), gitx.Client{}, r, list(r), bad, Local, nil); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
	// The directory exists although the listing does not know it (a race): not merged into.
	if err := os.MkdirAll(filepath.Join(r.CommonDir, "outfit", "profiles", "raced"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := Listing{}
	if _, err := Create(context.Background(), gitx.Client{}, r, stale, "raced", Local, nil); err == nil {
		t.Fatal("an existing directory was reused")
	}
}

func TestCreateRefusesProfilesWithPathsAnOpenFileCouldEscapeBy(t *testing.T) {
	r := fakeRepo(t)
	src := filepath.Join(r.CommonDir, "outfit", "profiles", "evil")
	write(t, filepath.Join(src, ".git/hooks/pre-commit"), "x", 0o755)
	l := list(r)
	e, _ := Resolve(l, "evil")
	if _, err := Create(context.Background(), gitx.Client{}, r, l, "copy", Local, &e); err == nil {
		t.Fatal("a profile with a file under .git was copied")
	}
	if _, err := os.Stat(filepath.Join(r.CommonDir, "outfit", "profiles", "copy")); err == nil {
		t.Fatal("half a profile was left behind")
	}
}

func TestPromoteMovesALocalProfileAndRefusesAClash(t *testing.T) {
	r := fakeRepo(t)
	src := filepath.Join(r.CommonDir, "outfit", "profiles", "mine")
	write(t, filepath.Join(src, "AGENTS.md"), "mine\n", 0o644)
	l := list(r)
	c, err := Promote(context.Background(), gitx.Client{Dir: r.WorkTree}, r, l, "mine")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(r.WorkTree, ".outfit", "profiles", "mine"); c.Dir != want {
		t.Fatalf("%s != %s", c.Dir, want)
	}
	if b, _ := os.ReadFile(filepath.Join(c.Dir, "AGENTS.md")); string(b) != "mine\n" {
		t.Fatalf("%q", b)
	}
	if _, err := os.Stat(src); err == nil {
		t.Fatal("the local copy is still there")
	}

	// A local profile shadowed by a committed one of the same name has nothing to promote.
	write(t, filepath.Join(r.CommonDir, "outfit", "profiles", "mine", "AGENTS.md"), "again\n", 0o644)
	if _, err := Promote(context.Background(), gitx.Client{Dir: r.WorkTree}, r, list(r), "mine"); err == nil {
		t.Fatal("promoted over a committed profile")
	}
	if b, _ := os.ReadFile(filepath.Join(r.CommonDir, "outfit", "profiles", "mine", "AGENTS.md")); string(b) != "again\n" {
		t.Fatal("the local copy was touched by a refused promote")
	}
}

func TestResolveReachesAShadowedProfileByTier(t *testing.T) {
	r := fakeRepo(t)
	write(t, filepath.Join(r.WorkTree, ".outfit/profiles/dup/AGENTS.md"), "c\n", 0o644)
	write(t, filepath.Join(r.CommonDir, "outfit", "profiles", "dup", "AGENTS.md"), "l\n", 0o644)
	l := list(r)
	if len(l.Shadowed) != 1 || !strings.Contains(l.Warnings[0].Message, "outfit new <other-name> --from local:dup") {
		t.Fatalf("%+v %+v", l.Shadowed, l.Warnings)
	}
	if p, err := Resolve(l, "dup"); err != nil || p.Tier != Committed {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := Resolve(l, "local:dup"); err != nil || p.Tier != Local {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := Resolve(l, "sideways:dup"); err == nil {
		t.Fatal("an unknown tier was accepted")
	}
	if _, err := Resolve(l, "worktree:dup"); err == nil {
		t.Fatal("a tier that does not have it was accepted")
	}
}
