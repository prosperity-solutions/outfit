package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/cli"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

// outfit runs one command inside dir with the fixture's hermetic environment.
func outfit(t *testing.T, f *testkit.Repo, dir string, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := map[string]string{}
	for _, kv := range f.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	// The hermetic environment must also reach the git child processes.
	for k, v := range env {
		t.Setenv(k, v)
	}
	code := cli.Execute(context.Background(), cli.Env{
		Build: cli.Build{Version: "test"}, Dir: dir, Getenv: func(k string) string { return env[k] },
		Stdout: &out, Stderr: &errb,
	}, args)
	return code, out.String(), errb.String()
}

func TestGetPrecedenceThroughRealGit(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	get := func(key string) (int, string) {
		code, out, _ := outfit(t, f, f.Dir, "get", key)
		return code, strings.TrimSpace(out)
	}

	// Team default (committed .outfit/config).
	if code, v := get("biome"); code != 0 || v != "on" {
		t.Fatalf("team default: %d %q", code, v)
	}
	// Unset key: empty and exit 1, so the shell fallback can take over.
	if code, v := get("nosuch"); code != 1 || v != "" {
		t.Fatalf("unset: %d %q", code, v)
	}
	// Active profile beats the team default.
	f.Git("config", "--local", "outfit.profile", "yolo")
	if code, v := get("biome"); code != 0 || v != "off" {
		t.Fatalf("profile: %d %q", code, v)
	}
	// --global beats the profile, --local beats --global, --worktree beats --local.
	f.Git("config", "--global", "outfit.biome", "global")
	if _, v := get("biome"); v != "global" {
		t.Fatalf("global: %q", v)
	}
	f.Git("config", "--local", "outfit.biome", "local")
	if _, v := get("biome"); v != "local" {
		t.Fatalf("local: %q", v)
	}
	f.Git("config", "extensions.worktreeConfig", "true")
	f.Git("config", "--worktree", "outfit.biome", "worktree")
	if _, v := get("biome"); v != "worktree" {
		t.Fatalf("worktree: %q", v)
	}
	// hook.<name> toggles resolve like any other key.
	f.Git("config", "--global", "outfit.hook.pre-commit", "off")
	if _, v := get("hook.pre-commit"); v != "off" {
		t.Fatalf("hook toggle: %q", v)
	}
}

func TestLockedKeyResistsEveryScope(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "extensions.worktreeConfig", "true")
	f.Git("config", "--local", "outfit.profile", "yolo")
	for _, scope := range []string{"--global", "--local", "--worktree"} {
		f.Git("config", scope, "outfit.secrets-scan", "off")
	}
	code, out, _ := outfit(t, f, f.Dir, "get", "secrets-scan", "--json")
	var r struct {
		Value  string `json:"value"`
		Source string `json:"source"`
		Locked bool   `json:"locked"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || code != 0 || r.Value != "on" || r.Source != "team" || !r.Locked {
		t.Fatalf("code=%d out=%q", code, out)
	}
	// `set` refuses a locked key and names the fix.
	code, _, errs := outfit(t, f, f.Dir, "set", "secrets-scan=off")
	if code != 1 || !strings.Contains(errs, "locked by .outfit/config") || !strings.Contains(errs, "fix:") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	// ...and `status` shows the ignored overrides as warnings.
	_, _, serr := outfit(t, f, f.Dir, "status")
	if !strings.Contains(serr, `"secrets-scan" is locked by .outfit/config`) {
		t.Fatalf("status warnings: %q", serr)
	}
}

func TestSetScopesAndUnset(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	if code, out, _ := outfit(t, f, f.Dir, "set", "biome=off"); code != 0 || out != "biome=off (local)\n" {
		t.Fatalf("local: %d %q", code, out)
	}
	if f.Git("config", "--local", "outfit.biome") != "off" {
		t.Fatal("default scope is --local")
	}
	if code, _, _ := outfit(t, f, f.Dir, "set", "tests=off", "hook.pre-commit=off", "--global"); code != 0 {
		t.Fatal("global")
	}
	if f.Git("config", "--global", "outfit.hook.pre-commit") != "off" {
		t.Fatal("hook key through git config")
	}
	// Idempotent: the same call twice is fine, and so is unsetting nothing.
	for i := 0; i < 2; i++ {
		if code, _, errs := outfit(t, f, f.Dir, "set", "tests="); code != 0 {
			t.Fatalf("unset #%d: %q", i, errs)
		}
	}
	if _, err := f.GitErr("config", "--local", "outfit.tests"); err == nil {
		t.Fatal("tests must be unset")
	}
	// --here needs the worktree extension, and says how to get it.
	code, _, errs := outfit(t, f, f.Dir, "set", "biome=on", "--here")
	if code != 1 || !strings.Contains(errs, "fix: outfit init") {
		t.Fatalf("--here without extension: %d %q", code, errs)
	}
	f.Git("config", "extensions.worktreeConfig", "true")
	if code, out, _ := outfit(t, f, f.Dir, "set", "biome=on", "--here"); code != 0 || out != "biome=on (worktree)\n" {
		t.Fatalf("--here: %d %q", code, out)
	}
	if f.Git("config", "--worktree", "outfit.biome") != "on" {
		t.Fatal("worktree scope")
	}
}

func TestSetValidation(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	cases := [][]string{
		{"set"},
		{"set", "novalue"},
		{"set", "Bad_Key=x"},
		{"set", "profile=yolo"},
		{"set", "locked=biome"},
		{"set", "biome=off", "--here", "--global"},
	}
	for _, args := range cases {
		code, _, errs := outfit(t, f, f.Dir, args...)
		if code != cli.ExitUsage || errs == "" {
			t.Errorf("%v: code=%d err=%q", args, code, errs)
		}
	}
	if _, err := f.GitErr("config", "--local", "--get-regexp", "^outfit\\."); err == nil {
		t.Fatal("nothing may have been written")
	}
}

func TestStatusAndLsThroughRealGit(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "--local", "outfit.profile", "yolo")
	f.Git("config", "--global", "outfit.biome", "maybe")

	code, out, errs := outfit(t, f, f.Dir, "status", "--json")
	if code != 0 {
		t.Fatalf("status: %d %q", code, errs)
	}
	var st struct {
		Profile struct{ Name, Source string }
		Tier    string
		Toggles []struct {
			Key, Value, Source string
			Locked             bool
		}
		Files []struct{ Path string }
	}
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	if st.Profile.Name != "yolo" || st.Profile.Source != "local" || st.Tier != "committed" || len(st.Files) != 2 {
		t.Fatalf("%+v", st)
	}
	got := map[string]string{}
	for _, tg := range st.Toggles {
		got[tg.Key] = tg.Value + "/" + tg.Source
	}
	if got["biome"] != "maybe/global" || got["tests"] != "off/profile" || got["secrets-scan"] != "on/team" {
		t.Fatalf("toggles = %v", got)
	}

	code, out, _ = outfit(t, f, f.Dir, "ls")
	if code != 0 || !strings.Contains(out, "yolo*") || !strings.Contains(out, "lean") || !strings.Contains(out, "committed") {
		t.Fatalf("ls: %d\n%s", code, out)
	}
	// A dangling profile name falls back to base with a warning, never an error.
	f.Git("config", "--local", "outfit.profile", "gone")
	code, out, errs = outfit(t, f, f.Dir, "status")
	if code != 0 || !strings.Contains(out, "profile  gone") || !strings.Contains(errs, "does not exist") {
		t.Fatalf("dangling: %d %q %q", code, out, errs)
	}
}

func TestStatusRefusesOldGit(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/git", []byte("#!/bin/sh\necho 'git version 2.30.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	var out, errb bytes.Buffer
	code := cli.Execute(context.Background(), cli.Env{Dir: f.Dir, Getenv: os.Getenv, Stdout: &out, Stderr: &errb}, []string{"status"})
	if code != 1 || !strings.Contains(errb.String(), "ppa:git-core/ppa") {
		t.Fatalf("code=%d err=%q", code, errb.String())
	}
}

// The budget of HANDOVER.md §2: `get` spawns at most one git process.
func TestGetSpawnsOneGitProcess(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "--local", "outfit.profile", "yolo")
	gitx.ResetCalls()
	if code, _, _ := outfit(t, f, f.Dir, "get", "biome"); code != 0 {
		t.Fatal("get")
	}
	if n := gitx.Calls(); n != 1 {
		t.Fatalf("get spawned %d git processes, budget is 1", n)
	}
}
