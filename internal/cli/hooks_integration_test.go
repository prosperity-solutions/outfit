package cli_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/cli"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

func hookEnv(f *testkit.Repo, dir string, stdin string) (cli.Env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	env := map[string]string{}
	for _, kv := range f.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	return cli.Env{
		Dir: dir, Getenv: func(k string) string { return env[k] },
		Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(stdin), Exe: "/usr/local/bin/outfit",
	}, &out, &errb
}

// Review-style invariant: whatever goes wrong inside outfit, the team's hook
// still runs. A secret scan must not vanish because a config read failed.
func TestHookFailsTowardRunningTheTeamHook(t *testing.T) {
	f := testkit.New(t, testkit.Team, testkit.WithHooks())
	if err := os.WriteFile(f.Path(".git/hooks/pre-push"), []byte("#!/bin/sh\necho team-ran\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range testkit.EnvMap(f.Home) {
		t.Setenv(k, v)
	}
	// git refuses the repository's config (dubious ownership): load fails.
	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
	env, out, errb := hookEnv(f, f.Dir, "")
	env.Getenv = os.Getenv
	code := cli.Execute(context.Background(), env, []string{"hook", "pre-push", "origin", "url"})
	if code != 3 || !strings.Contains(out.String(), "team-ran") {
		t.Fatalf("code=%d out=%q err=%q: the team hook must run and its exit code win", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "running the team's pre-push hook anyway") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestHookRejectsNamesGitDoesNotHave(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	env, _, errb := hookEnv(f, f.Dir, "")
	for _, args := range [][]string{{"hook"}, {"hook", "../../bin/sh"}, {"hook", "pre-commmit"}} {
		if code := cli.Execute(context.Background(), env, args); code != cli.ExitUsage || errb.Len() == 0 {
			t.Errorf("%v: code=%d", args, code)
		}
		errb.Reset()
	}
}

func TestInitRefusesAForeignWorktreeHooksPath(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	f.Git("config", "extensions.worktreeConfig", "true")
	f.Git("config", "--worktree", "core.hooksPath", "/somebody/elses/hooks")
	for k, v := range testkit.EnvMap(f.Home) {
		t.Setenv(k, v)
	}
	env, _, errb := hookEnv(f, f.Dir, "")
	env.Getenv = os.Getenv
	if code := cli.Execute(context.Background(), env, []string{"init"}); code != 1 || !strings.Contains(errb.String(), "already set") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
	if f.Git("config", "--worktree", "core.hooksPath") != "/somebody/elses/hooks" {
		t.Fatal("init must not overwrite someone else's worktree hooksPath")
	}
}

func TestInitRefusesOldGit(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/git", []byte("#!/bin/sh\necho 'git version 2.30.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	env, _, errb := hookEnv(f, f.Dir, "")
	env.Getenv = os.Getenv
	if code := cli.Execute(context.Background(), env, []string{"init"}); code != 1 || !strings.Contains(errb.String(), "ppa:git-core/ppa") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

func TestInitJSONShape(t *testing.T) {
	f := testkit.New(t, testkit.Team, testkit.WithHusky())
	for k, v := range testkit.EnvMap(f.Home) {
		t.Setenv(k, v)
	}
	env, out, _ := hookEnv(f, f.Dir, "")
	env.Getenv = os.Getenv
	if code := cli.Execute(context.Background(), env, []string{"init", "--dry-run", "--json"}); code != 0 {
		t.Fatalf("code=%d", code)
	}
	for _, key := range []string{`"dry_run":true`, `"changed":true`, `"hooks_dir":`, `"original_hooks_dir":`, `"shims":[`, `"removed_shims":[]`, `"worktrees":[`, `"enabled_worktree_config":true`} {
		if !strings.Contains(out.String(), key) {
			t.Errorf("missing %s in %s", key, out.String())
		}
	}
	if _, err := os.Stat(f.Path(".git/outfit")); err == nil {
		t.Fatal("--dry-run must write nothing")
	}
}
