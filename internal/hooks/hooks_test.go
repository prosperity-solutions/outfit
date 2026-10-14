package hooks

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/config"
)

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestShimsCoverEveryClientHookNotJustTheOnesTheTeamHasToday(t *testing.T) {
	// A fixed set chosen at init would silently skip any hook added later (a merged
	// PR adding a secret scan; `npm install` creating husky's directory).
	got := ShimNames(filepath.Join(t.TempDir(), "no-team-hooks"))
	for _, must := range []string{"pre-commit", "commit-msg", "pre-push", "prepare-commit-msg", "post-checkout", "post-merge", "post-rewrite", "pre-rebase", "post-commit", "pre-merge-commit"} {
		if !slices.Contains(got, must) {
			t.Errorf("no shim for %s", must)
		}
	}
	// The hot hooks run on nearly every ref update or index write: only when the team has one.
	for _, hot := range Hot {
		if slices.Contains(got, hot) {
			t.Errorf("%s must not be shimmed by default", hot)
		}
	}
	dir := t.TempDir()
	write(t, filepath.Join(dir, "reference-transaction"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(dir, "post-index-change"), "#!/bin/sh\n", 0o644) // not executable
	with := ShimNames(dir)
	if !slices.Contains(with, "reference-transaction") || slices.Contains(with, "post-index-change") {
		t.Fatalf("hot hooks follow the team: %v", with)
	}
}

// Without the binary, the shim runs the team's hook, found AT RUN TIME with the
// same rule as OriginalDir (never from a path baked in at init: husky may set
// core.hooksPath later).
func TestShimIsValidPOSIXShAndRunsTheTeamHookWhenOutfitIsGone(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	team := filepath.Join(home, "team hooks")
	write(t, filepath.Join(team, "pre-push"), "#!/bin/sh\necho \"team hook: $*\" >&2\ncat > "+filepath.Join(dir, "stdin")+"\nexit 5\n", 0o755)
	write(t, filepath.Join(home, ".gitconfig"), "[core]\n\thooksPath = ~/team hooks\n", 0o644)
	shim := filepath.Join(dir, "ours", "pre-push")
	write(t, shim, Shim("pre-push", filepath.Join(dir, "no-such-outfit")), 0o755)

	if sc, err := exec.LookPath("shellcheck"); err == nil {
		if out, err := exec.Command(sc, "-s", "sh", shim).CombinedOutput(); err != nil {
			t.Fatalf("shellcheck: %v\n%s", err, out)
		}
	}
	if err := exec.Command("sh", "-n", shim).Run(); err != nil {
		t.Fatalf("not valid sh: %v", err)
	}

	cmd := exec.Command(shim, "origin", "url with spaces")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1"}
	cmd.Stdin = strings.NewReader("refs/heads/main abc refs/heads/x def\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 5 {
		t.Fatalf("exit code must be the team hook's (5): %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "binary not found") || !strings.Contains(stderr.String(), "team hook: origin url with spaces") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "stdin")); !strings.Contains(string(b), "refs/heads/main") {
		t.Fatalf("stdin must reach the team hook: %q", b)
	}
}

func TestShimQuotesHostilePaths(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	evil := filepath.Join(home, "a'b $(touch pwned) `x`")
	write(t, filepath.Join(evil, "pre-commit"), "#!/bin/sh\nexit 0\n", 0o755)
	write(t, filepath.Join(home, ".gitconfig"), "[core]\n\thooksPath = "+evil+"\n", 0o644)
	gone := filepath.Join(dir, "gone '$(touch pwned2)'")
	shim := filepath.Join(dir, "shim")
	write(t, shim, Shim("pre-commit", gone), 0o755)
	cmd := exec.Command(shim)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_GLOBAL=" + filepath.Join(home, ".gitconfig"), "GIT_CONFIG_NOSYSTEM=1"}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"pwned", "pwned2"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Fatalf("a path was interpreted by the shell (%s)", f)
		}
	}
}

func TestOriginalDirPrecedenceAndOurExclusion(t *testing.T) {
	ours := "/repo/.git/outfit/hooks"
	gc := func(es ...config.Entry) config.GitConfig { return config.GitConfig{HooksPaths: es} }
	e := func(scope, v string) config.Entry { return config.Entry{Scope: scope, Key: "core.hookspath", Value: v} }

	cases := []struct {
		name string
		gc   config.GitConfig
		dir  string
	}{
		{"nothing: the common hooks dir", gc(), "/repo/.git/hooks"},
		{"local relative is relative to the worktree", gc(e("local", ".husky/_")), "/repo/.husky/_"},
		{"local beats global", gc(e("global", "/g"), e("local", "/l")), "/l"},
		{"global beats system", gc(e("system", "/s"), e("global", "/g")), "/g"},
		{"ours at worktree scope is never the original", gc(e("worktree", ours), e("local", "/l")), "/l"},
		{"ours at local scope is skipped", gc(e("local", ours), e("global", "/g")), "/g"},
		{"home", gc(e("global", "~/hooks")), "/home/me/hooks"},
	}
	for _, c := range cases {
		if dir := OriginalDir(c.gc, ours, "/repo", "/repo/.git", "/home/me"); dir != c.dir {
			t.Errorf("%s: got %q want %q", c.name, dir, c.dir)
		}
	}
}

func TestChainPassesArgsStdinAndExactExitCode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\necho \"args=$*\"; cat; exit 42\n", 0o755)
	var out, errb bytes.Buffer
	code, ran := Chain(context.Background(), dir, "pre-push", "pre-push@x", []string{"origin", "u r l"}, strings.NewReader("stdin-data"), &out, &errb)
	if !ran || code != 42 || out.String() != "args=origin u r l\nstdin-data" {
		t.Fatalf("code=%d ran=%v out=%q err=%q", code, ran, out.String(), errb.String())
	}
}

func TestChainMissingOrNonExecutableIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "commit-msg"), "#!/bin/sh\nexit 1\n", 0o644)
	for _, name := range []string{"commit-msg", "pre-push"} {
		if code, ran := Chain(context.Background(), dir, name, name+"@x", nil, nil, os.Stdout, os.Stderr); code != 0 || ran {
			t.Errorf("%s: code=%d ran=%v", name, code, ran)
		}
	}
	// A hook that cannot start is reported like git does: 126.
	write(t, filepath.Join(dir, "post-merge"), "#!/no/such/interpreter\n", 0o755)
	if code, ran := Chain(context.Background(), dir, "post-merge", "post-merge@x", nil, nil, os.Stdout, os.Stderr); !ran || code != 126 {
		t.Errorf("code=%d ran=%v", code, ran)
	}
}

func TestChainReportsSignalDeathAs128PlusSignal(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-commit"), "#!/bin/sh\nkill -TERM $$\n", 0o755)
	if code, _ := Chain(context.Background(), dir, "pre-commit", "pre-commit@x", nil, nil, os.Stdout, os.Stderr); code != 128+15 {
		t.Fatalf("code=%d", code)
	}
}

func TestGeneratedOnlyListsOurShims(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-commit"), Shim("pre-commit", "/bin/outfit"), 0o755)
	write(t, filepath.Join(dir, "commit-msg"), "#!/bin/sh\n# the team's own\n", 0o755)
	if got := Generated(dir); !reflect.DeepEqual(got, []string{"pre-commit"}) {
		t.Fatalf("got %v", got)
	}
}

func TestForeignListsHooksAnotherToolInstalled(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-commit"), Shim("pre-commit", "/bin/outfit"), 0o755)
	write(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\n# lefthook\n", 0o755)
	write(t, filepath.Join(dir, "pre-commit.old"), "#!/bin/sh\n", 0o755) // not a hook name
	if got := Foreign(dir); !reflect.DeepEqual(got, []string{"pre-push"}) {
		t.Fatalf("got %v", got)
	}
}

// A team "hook" that is really our shim (a symlink, a hooksPath that loops) must
// not be chained into: that forks forever.
func TestChainRefusesOurOwnShims(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "real-shim"), Shim("pre-commit", "/bin/outfit"), 0o755)
	if err := os.Symlink(filepath.Join(dir, "real-shim"), filepath.Join(dir, "pre-commit")); err != nil {
		t.Fatal(err)
	}
	var errb bytes.Buffer
	if code, ran := Chain(context.Background(), dir, "pre-commit", "pre-commit@x", nil, nil, os.Stdout, &errb); ran || code != 0 || !strings.Contains(errb.String(), "outfit shim") {
		t.Fatalf("code=%d ran=%v err=%q", code, ran, errb.String())
	}
}

func TestSameDirComparesInodesNotSpelling(t *testing.T) {
	dir := t.TempDir()
	d := filepath.Join(dir, "Hooks")
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias")
	if err := os.Symlink(d, link); err != nil {
		t.Fatal(err)
	}
	if !sameDir(d, link) || sameDir(d, dir) {
		t.Fatal("symlinks are the same directory, a parent is not")
	}
	// On a case-insensitive filesystem (APFS) a differently-cased spelling is the same directory too.
	if _, err := os.Stat(filepath.Join(dir, "hooks")); err == nil && !sameDir(d, filepath.Join(dir, "hooks")) {
		t.Fatal("case variants of one directory must compare equal where the filesystem folds case")
	}
}

func TestHooksPathFromFilesWorksWithoutGit(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	write(t, a, "[core]\n\tbare = false\n[Core \"x\"]\n\thookspath = /ignored\n", 0o644)
	write(t, b, "[user]\n\tname = x\n[core]\n\thooksPath = \"/team/hooks\"\n", 0o644)
	if got := HooksPathFromFiles(filepath.Join(dir, "missing"), a, b); got != "/team/hooks" {
		t.Fatalf("got %q", got)
	}
	write(t, filepath.Join(dir, "empty-section"), "[]\n[ ]\n", 0o644)
	if got := HooksPathFromFiles(filepath.Join(dir, "empty-section")); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestIsOutfitDirRecognisesOwnAndStaleDirectories(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "old", ".git", "outfit", "hooks") // a moved repository leaves exactly this
	if !IsOutfitDir(gone) {
		t.Fatal("a missing .../outfit/hooks is outfit's stale value")
	}
	live := filepath.Join(dir, "r", ".git", "outfit", "hooks")
	write(t, filepath.Join(live, "pre-commit"), Shim("pre-commit", "/bin/outfit"), 0o755)
	if !IsOutfitDir(live) {
		t.Fatal("a directory holding outfit shims is outfit's")
	}
	// A team directory that merely has the same last components, but real team hooks.
	team := filepath.Join(dir, "t", "outfit", "hooks")
	write(t, filepath.Join(team, "pre-commit"), "#!/bin/sh\n", 0o755)
	if IsOutfitDir(team) {
		t.Fatal("a directory with team hooks and no shims is not outfit's")
	}
	if IsOutfitDir(filepath.Join(dir, "husky", "_")) {
		t.Fatal("other names never are")
	}
}

func TestLeftoversAreOurShimsRenamedOut(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-commit.old"), Shim("pre-commit", "/bin/outfit"), 0o755)
	write(t, filepath.Join(dir, "pre-push.old"), "#!/bin/sh\n# lefthook's own\n", 0o755)
	write(t, filepath.Join(dir, "notahook.old"), Shim("pre-commit", "/bin/outfit"), 0o755)
	if got := Leftovers(dir); !reflect.DeepEqual(got, []string{"pre-commit.old"}) {
		t.Fatalf("got %v", got)
	}
}

func TestOriginalDirNeverReturnsAnOutfitDirectory(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "old", ".git", "outfit", "hooks")
	gc := config.GitConfig{HooksPaths: []config.Entry{{Scope: "local", Key: "core.hookspath", Value: stale}, {Scope: "global", Key: "core.hookspath", Value: "/g"}}}
	if got := OriginalDir(gc, filepath.Join(dir, "new", ".git", "outfit", "hooks"), dir, filepath.Join(dir, ".git"), "/h"); got != "/g" {
		t.Fatalf("a stale outfit path someone copied into --local must be skipped: %q", got)
	}
}

func TestActiveKeyIsPerRepository(t *testing.T) {
	if ActiveKey("pre-commit", "/a/.git") == ActiveKey("pre-commit", "/b/.git") || ActiveKey("pre-commit", "/a/.git") == ActiveKey("pre-push", "/a/.git") {
		t.Fatal("the marker must distinguish repositories and hooks")
	}
	if ActiveKey("pre-commit", "/a/.git/") != ActiveKey("pre-commit", "/a/.git") {
		t.Fatal("spelling of the path must not matter")
	}
}

// Row 30 of docs/git-behavior.md: git decides a hook's cwd (the worktree root for
// client hooks, $GIT_DIR for receive-side ones), so Chain must inherit it.
func TestChainInheritsTheCallersWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "hooks", "post-update"), "#!/bin/sh\npwd -P\n", 0o755)
	here := filepath.Join(dir, "gitdir")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(here)
	var out bytes.Buffer
	if code, ran := Chain(context.Background(), filepath.Join(dir, "hooks"), "post-update", "post-update@x", nil, nil, &out, os.Stderr); code != 0 || !ran {
		t.Fatalf("code=%d ran=%v", code, ran)
	}
	want, _ := filepath.EvalSymlinks(here)
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("cwd = %q, want %q", got, want)
	}
}
