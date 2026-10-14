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
	if code != 1 || !strings.Contains(errs, "fix: git config extensions.worktreeConfig true") {
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

// Review finding: .outfit itself replaced by a symlink to files outside the repo.
func TestSymlinkedDotOutfitIsNotRead(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	outside := t.TempDir()
	if err := os.WriteFile(outside+"/config", []byte("[outfit]\n biome = fromoutside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(f.Path(".outfit")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, f.Path(".outfit")); err != nil {
		t.Fatal(err)
	}
	code, out, _ := outfit(t, f, f.Dir, "get", "biome")
	if code != 1 || strings.Contains(out, "fromoutside") {
		t.Fatalf("code=%d out=%q", code, out)
	}
	_, _, errs := outfit(t, f, f.Dir, "status")
	if !strings.Contains(errs, "real directory") {
		t.Fatalf("status must say why: %q", errs)
	}
}

// Review finding (threat model): a hook in a linked worktree that `cd`s into a
// package must still see the ROOT .outfit/ (and its locks), not a nested one.
func TestGetFromHookInSubdirectoryIgnoresNestedDotOutfit(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo, testkit.WithWorktrees(1))
	wt := f.Worktrees[0]
	nested := wt + "/apps/web/.outfit"
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested+"/config", []byte("[outfit]\n secrets-scan = off\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDir := f.GitIn(wt, "rev-parse", "--path-format=absolute", "--git-dir")
	var out, errb bytes.Buffer
	for k, v := range testkit.EnvMap(f.Home) {
		t.Setenv(k, v)
	}
	code := cli.Execute(context.Background(), cli.Env{
		Dir: wt + "/apps/web",
		Getenv: func(k string) string {
			if k == "GIT_DIR" {
				return gitDir
			}
			return os.Getenv(k)
		},
		Stdout: &out, Stderr: &errb,
	}, []string{"get", "secrets-scan"})
	if code != 0 || strings.TrimSpace(out.String()) != "on" {
		t.Fatalf("code=%d out=%q err=%q: the root lock must win", code, out.String(), errb.String())
	}
}

// `git -c outfit.x=y <cmd>` reaches hooks as the command scope and beats every
// other source except a locked team value.
func TestCommandScopeBeatsEverythingButLocks(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "--local", "outfit.biome", "local")
	t.Setenv("GIT_CONFIG_COUNT", "2")
	t.Setenv("GIT_CONFIG_KEY_0", "outfit.biome")
	t.Setenv("GIT_CONFIG_VALUE_0", "once")
	t.Setenv("GIT_CONFIG_KEY_1", "outfit.secrets-scan")
	t.Setenv("GIT_CONFIG_VALUE_1", "off")
	var out, errb bytes.Buffer
	run := func(key string) string {
		out.Reset()
		cli.Execute(context.Background(), cli.Env{Dir: f.Dir, Getenv: os.Getenv, Stdout: &out, Stderr: &errb}, []string{"get", key})
		return strings.TrimSpace(out.String())
	}
	for k, v := range testkit.EnvMap(f.Home) {
		t.Setenv(k, v)
	}
	if got := run("biome"); got != "once" {
		t.Fatalf("command scope = %q", got)
	}
	if got := run("secrets-scan"); got != "on" {
		t.Fatalf("a locked key resists `git -c`: %q", got)
	}
}

func TestFlagErrorsAreJSONWhenAsked(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	code, out, errs := outfit(t, f, f.Dir, "get", "biome", "--bogus", "--json")
	var e struct {
		Error string
		Fix   string
	}
	if code != cli.ExitUsage || json.Unmarshal([]byte(out), &e) != nil || e.Fix != "outfit get -h" || errs != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, _ := outfit(t, f, f.Dir, "get", "-h"); code != 0 || !strings.Contains(out, "usage: outfit get") {
		t.Fatalf("-h: %d %q", code, out)
	}
}

func TestLsJSONIncludesBaseAndInactiveWarnings(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Write(".outfit/profiles/lean/profile.config", "[outfit]\n biome = off\n[core]\n fsmonitor = x\n")
	_, out, errs := outfit(t, f, f.Dir, "ls", "--json")
	var ls struct {
		Active   string
		Profiles []struct{ Name, Tier string }
	}
	if err := json.Unmarshal([]byte(out), &ls); err != nil || ls.Active != "base" || ls.Profiles[0].Name != "base" || ls.Profiles[0].Tier != "builtin" {
		t.Fatalf("%v: %q", err, out)
	}
	if !strings.Contains(out, "core.fsmonitor") || errs != "" {
		t.Fatalf("a broken INACTIVE profile must raise a warning: %q", out)
	}
}

func TestStatusShowsLockedWithoutValueAndMissingProfile(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Write(".outfit/config", "[outfit]\n locked = nope-never-set\n")
	f.Git("config", "--local", "outfit.profile", "ghost")
	code, out, _ := outfit(t, f, f.Dir, "status")
	if code != 0 || !strings.Contains(out, "nope-never-set=(unset) (locked by team)") || !strings.Contains(out, "profile  ghost (not found: behaving as base") {
		t.Fatalf("%d\n%s", code, out)
	}
}

// Review finding: a repo with only .outfit/config (no profiles/) is the normal
// §3.0 layout and must not look like a symlink attack.
func TestNoProfilesDirectoryIsNotAWarning(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	if err := os.RemoveAll(f.Path(".outfit/profiles")); err != nil {
		t.Fatal(err)
	}
	f.Git("add", "-A")
	f.Git("commit", "-qm", "chore: no profiles")
	for _, cmd := range []string{"status", "ls"} {
		code, _, errs := outfit(t, f, f.Dir, cmd)
		if code != 0 || errs != "" {
			t.Fatalf("%s: code=%d stderr=%q", cmd, code, errs)
		}
	}
}

func TestSetNormalizesBooleansAndReportsScopes(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	for in, want := range map[string]string{"OFF": "off", "false": "off", "No": "off", "0": "off", "TRUE": "on", "yes": "on", "fast": "fast"} {
		outfit(t, f, f.Dir, "set", "biome="+in)
		if got := f.Git("config", "--local", "outfit.biome"); got != want {
			t.Errorf("set biome=%s stored %q, want %q", in, got, want)
		}
	}
	// Undo is scope-aware and says so.
	outfit(t, f, f.Dir, "set", "biome=off", "--global")
	_, out, _ := outfit(t, f, f.Dir, "set", "biome=")
	if out != "biome unset (local); still set in: global\n" {
		t.Fatalf("got %q", out)
	}
	_, out, _ = outfit(t, f, f.Dir, "set", "biome=")
	if out != "biome was not set (local); still set in: global\n" {
		t.Fatalf("second unset: %q", out)
	}
	_, out, _ = outfit(t, f, f.Dir, "set", "nonexistent=")
	if out != "nonexistent was not set (local)\n" {
		t.Fatalf("%q", out)
	}
	// JSON is an object with stable fields.
	_, out, _ = outfit(t, f, f.Dir, "set", "tests=off", "--json")
	var r struct {
		Changes []map[string]any
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.Changes) != 1 {
		t.Fatalf("%v %q", err, out)
	}
	if r.Changes[0]["still_set_in"] == nil {
		t.Error("still_set_in must be [], never null")
	}
	// Setting the same value twice reports the second as unchanged.
	if _, o2, _ := outfit(t, f, f.Dir, "set", "tests=off", "--json"); !strings.Contains(o2, `"changed":false`) {
		t.Errorf("repeated set must be idempotent: %q", o2)
	}
	for _, k := range []string{"key", "value", "unset", "scope", "changed", "still_set_in"} {
		if _, ok := r.Changes[0][k]; !ok {
			t.Errorf("change is missing field %q: %v", k, r.Changes[0])
		}
	}
}

func TestSetConvergesWhenAKeyHoldsSeveralValues(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "--local", "--add", "outfit.biome", "a")
	f.Git("config", "--local", "--add", "outfit.biome", "b")
	if code, _, errs := outfit(t, f, f.Dir, "set", "biome=off"); code != 0 {
		t.Fatalf("code=%d %q", code, errs)
	}
	if got := f.Git("config", "--local", "--get-all", "outfit.biome"); got != "off" {
		t.Fatalf("got %q", got)
	}
}

func TestSetGlobalNeedsNoRepository(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	elsewhere := t.TempDir()
	code, out, errs := outfit(t, f, elsewhere, "set", "biome=off", "--global")
	if code != 0 || out != "biome=off (global)\n" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	if f.Git("config", "--global", "outfit.biome") != "off" {
		t.Fatal("not written")
	}
	// ...but a repo-scoped write outside a repo still says what to do.
	code, _, errs = outfit(t, f, elsewhere, "set", "biome=off")
	if code != 1 || !strings.Contains(errs, "fix: cd into your repository") {
		t.Fatalf("code=%d %q", code, errs)
	}
}

func TestSetReportsRetryableLockContention(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	if err := os.WriteFile(f.Path(".git/config.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, _ := outfit(t, f, f.Dir, "set", "biome=off", "--json")
	var e struct {
		Retryable bool
		Fix       string
	}
	if code != 1 || json.Unmarshal([]byte(out), &e) != nil || !e.Retryable || e.Fix == "" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

// Review finding: when git refuses to read the repo's config (safe.directory),
// `get` must fail loudly instead of returning the team value.
func TestDubiousOwnershipFailsLoudly(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "--local", "outfit.biome", "mine")
	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
	code, _, errs := outfit(t, f, f.Dir, "get", "biome")
	if code != 1 || !strings.Contains(errs, "safe.directory") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

// Review finding: a hook's GIT_DIR may be relative to a cwd we have since left.
func TestRelativeGitDirFromSubdirectory(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "--local", "outfit.biome", "mine")
	for k, v := range testkit.EnvMap(f.Home) {
		t.Setenv(k, v)
	}
	var out, errb bytes.Buffer
	code := cli.Execute(context.Background(), cli.Env{
		Dir: f.Dir + "/apps/web",
		Getenv: func(k string) string {
			if k == "GIT_DIR" {
				return "../../.git"
			}
			return os.Getenv(k)
		},
		Stdout: &out, Stderr: &errb,
	}, []string{"get", "biome"})
	if code != 0 || strings.TrimSpace(out.String()) != "mine" {
		t.Fatalf("code=%d out=%q err=%q: the local override must be seen", code, out.String(), errb.String())
	}
}

func TestLsOnlyListsRealCommittedTrees(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	outside := t.TempDir()
	if err := os.WriteFile(outside+"/profile.config", []byte("[outfit]\n secret = outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.Write(".outfit/profiles/readme.md", "not a profile\n")
	if err := os.Symlink(outside, f.Path(".outfit/profiles/evil")); err != nil {
		t.Fatal(err)
	}
	f.Git("add", "-A")
	f.Git("commit", "-qm", "chore: junk in profiles")
	// Hide both from disk like an inactive profile, so ls must go through HEAD.
	_ = os.RemoveAll(f.Path(".outfit/profiles/evil"))
	_ = os.Remove(f.Path(".outfit/profiles/readme.md"))
	_, out, _ := outfit(t, f, f.Dir, "ls")
	if strings.Contains(out, "evil") || strings.Contains(out, "readme") || strings.Contains(out, "outside") {
		t.Fatalf("only trees are profiles:\n%s", out)
	}
}

func TestInvalidProfileDirectoryNamesAreReported(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Write(".outfit/profiles/Has Space/profile.config", "[outfit]\n x = on\n")
	_, out, errs := outfit(t, f, f.Dir, "ls")
	if strings.Contains(out, "Has Space") || !strings.Contains(errs, "not a valid profile name") {
		t.Fatalf("out=%q err=%q", out, errs)
	}
}

func TestLockWarningsAreInStableOrder(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Write(".outfit/config", "[outfit]\n locked = a, b, c, d\n a = on\n b = on\n c = on\n d = on\n")
	for _, k := range []string{"d", "b", "a", "c"} {
		f.Git("config", "--local", "outfit."+k, "off")
	}
	var first string
	for i := 0; i < 8; i++ {
		_, _, errs := outfit(t, f, f.Dir, "status")
		if i == 0 {
			first = errs
		} else if errs != first {
			t.Fatalf("warning order changed:\n%s\n---\n%s", first, errs)
		}
	}
}

func TestExtensionWorktreeConfigOnlyCountsInTheRepoConfig(t *testing.T) {
	f := testkit.New(t, testkit.Monorepo)
	f.Git("config", "--global", "extensions.worktreeConfig", "true")
	code, _, errs := outfit(t, f, f.Dir, "set", "biome=off", "--here")
	if code != 1 || !strings.Contains(errs, "extensions.worktreeConfig") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}
