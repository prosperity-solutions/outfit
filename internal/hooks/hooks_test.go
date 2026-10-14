package hooks

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

func TestShimNamesAreTriggersPlusTeamHooks(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "commit-msg"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\n", 0o755)
	write(t, filepath.Join(dir, "pre-rebase.sample"), "#!/bin/sh\n", 0o755) // a sample
	write(t, filepath.Join(dir, "update"), "#!/bin/sh\n", 0o644)            // not executable
	write(t, filepath.Join(dir, "h"), "#!/bin/sh\n", 0o755)                 // husky's helper
	want := []string{"commit-msg", "post-checkout", "post-merge", "post-rewrite", "pre-commit", "pre-push"}
	if got := ShimNames(dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if got := ShimNames(filepath.Join(dir, "missing")); !reflect.DeepEqual(got, []string{"post-checkout", "post-merge", "post-rewrite", "pre-commit"}) {
		t.Fatalf("no team hooks: %v", got)
	}
	// reference-transaction runs on every ref update: never shimmed unless the team has it.
	for _, n := range ShimNames(t.TempDir()) {
		if n == "reference-transaction" {
			t.Fatal("must not shim reference-transaction by default")
		}
	}
}

func TestShimIsValidPOSIXShAndRunsTheTeamHookWhenOutfitIsGone(t *testing.T) {
	dir := t.TempDir()
	team := filepath.Join(dir, "team")
	write(t, filepath.Join(team, "pre-push"), "#!/bin/sh\necho \"team hook: $*\" >&2\ncat > "+filepath.Join(dir, "stdin")+"\nexit 5\n", 0o755)
	shim := filepath.Join(dir, "shim")
	write(t, shim, Shim("pre-push", filepath.Join(dir, "no-such-outfit"), team), 0o755)

	if sc, err := exec.LookPath("shellcheck"); err == nil {
		if out, err := exec.Command(sc, "-s", "sh", shim).CombinedOutput(); err != nil {
			t.Fatalf("shellcheck: %v\n%s", err, out)
		}
	}
	if err := exec.Command("sh", "-n", shim).Run(); err != nil {
		t.Fatalf("not valid sh: %v", err)
	}

	cmd := exec.Command(shim, "origin", "url with spaces")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	cmd.Stdin = strings.NewReader("refs/heads/main abc refs/heads/x def\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	ee, ok := err.(*exec.ExitError)
	if !ok || ee.ExitCode() != 5 {
		t.Fatalf("exit code must be the team hook's (5): %v", err)
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
	evil := filepath.Join(dir, "a'b $(touch pwned) `x`")
	write(t, filepath.Join(evil, "pre-commit"), "#!/bin/sh\nexit 0\n", 0o755)
	shim := filepath.Join(dir, "shim")
	write(t, shim, Shim("pre-commit", filepath.Join(dir, "gone"), evil), 0o755)
	cmd := exec.Command(shim)
	cmd.Dir = dir
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
		t.Fatal("a path was interpreted by the shell")
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
		raw  string
	}{
		{"nothing: the common hooks dir", gc(), "/repo/.git/hooks", "/repo/.git/hooks"},
		{"local relative is relative to the worktree", gc(e("local", ".husky/_")), "/repo/.husky/_", ".husky/_"},
		{"local beats global", gc(e("global", "/g"), e("local", "/l")), "/l", "/l"},
		{"global beats system", gc(e("system", "/s"), e("global", "/g")), "/g", "/g"},
		{"ours at worktree scope is never the original", gc(e("worktree", ours), e("local", "/l")), "/l", "/l"},
		{"ours at local scope is skipped", gc(e("local", ours), e("global", "/g")), "/g", "/g"},
		{"home", gc(e("global", "~/hooks")), "/home/me/hooks", "~/hooks"},
	}
	for _, c := range cases {
		dir, raw := OriginalDir(c.gc, ours, "/repo", "/repo/.git", "/home/me")
		if dir != c.dir || raw != c.raw {
			t.Errorf("%s: got (%q, %q) want (%q, %q)", c.name, dir, raw, c.dir, c.raw)
		}
	}
}

func TestChainPassesArgsStdinAndExactExitCode(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-push"), "#!/bin/sh\necho \"args=$*\"; cat; exit 42\n", 0o755)
	var out, errb bytes.Buffer
	code, ran := Chain(context.Background(), dir, "pre-push", []string{"origin", "u r l"}, strings.NewReader("stdin-data"), &out, &errb, dir)
	if !ran || code != 42 || out.String() != "args=origin u r l\nstdin-data" {
		t.Fatalf("code=%d ran=%v out=%q err=%q", code, ran, out.String(), errb.String())
	}
}

func TestChainMissingOrNonExecutableIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "commit-msg"), "#!/bin/sh\nexit 1\n", 0o644)
	for _, name := range []string{"commit-msg", "pre-push"} {
		if code, ran := Chain(context.Background(), dir, name, nil, nil, os.Stdout, os.Stderr, dir); code != 0 || ran {
			t.Errorf("%s: code=%d ran=%v", name, code, ran)
		}
	}
	// A hook that cannot start is reported like git does: 126.
	write(t, filepath.Join(dir, "post-merge"), "#!/no/such/interpreter\n", 0o755)
	if code, ran := Chain(context.Background(), dir, "post-merge", nil, nil, os.Stdout, os.Stderr, dir); !ran || code != 126 {
		t.Errorf("code=%d ran=%v", code, ran)
	}
}

func TestChainReportsSignalDeathAs128PlusSignal(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-commit"), "#!/bin/sh\nkill -TERM $$\n", 0o755)
	if code, _ := Chain(context.Background(), dir, "pre-commit", nil, nil, os.Stdout, os.Stderr, dir); code != 128+15 {
		t.Fatalf("code=%d", code)
	}
}

func TestGeneratedOnlyListsOurShims(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "pre-commit"), Shim("pre-commit", "/bin/outfit", "/x"), 0o755)
	write(t, filepath.Join(dir, "commit-msg"), "#!/bin/sh\n# the team's own\n", 0o755)
	if got := Generated(dir); !reflect.DeepEqual(got, []string{"pre-commit"}) {
		t.Fatalf("got %v", got)
	}
}
