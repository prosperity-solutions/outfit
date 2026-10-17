package testkit

import (
	"crypto/sha1" //nolint:gosec // git object ids are SHA-1
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/prosperity-solutions/outfit/internal/gitx"
)

// ScriptSetup is testscript's Setup: the hermetic environment of §7.2 rooted in
// the script's work dir.
func ScriptSetup(env *testscript.Env) error {
	home := filepath.Join(env.WorkDir, "home")
	if err := WriteHome(home); err != nil {
		return err
	}
	m := EnvMap(home)
	m[HookLogEnv] = filepath.Join(env.WorkDir, "hooks.log")
	m[gitx.TraceEnv] = filepath.Join(env.WorkDir, "git-trace.log")
	for k, v := range m {
		env.Vars = append(env.Vars, k+"="+v)
	}
	// PATHNOOUTFIT is PATH without any directory that holds an `outfit` (the one
	// testscript.Main adds): a machine that never installed outfit.
	var kept []string
	for _, d := range filepath.SplitList(m["PATH"]) {
		if _, err := os.Stat(filepath.Join(d, "outfit")); err != nil {
			kept = append(kept, d)
		}
	}
	env.Vars = append(env.Vars, "PATHNOOUTFIT="+strings.Join(kept, string(os.PathListSeparator)))
	return nil
}

// ScriptCommands are the custom testscript commands of HANDOVER.md §7.1.
func ScriptCommands() map[string]func(*testscript.TestScript, bool, []string) {
	return map[string]func(*testscript.TestScript, bool, []string){
		"atomicsave": cmdAtomicSave,
		"islink":     cmdIsLink,
		"samefile":   cmdSameFile,
		"blobsha":    cmdBlobSHA,
		"gitver":     cmdGitVer,
		"hooklog":    cmdHookLog,
		"gitcalls":   cmdGitCalls,
		"fixture":    cmdFixture,
	}
}

// ScriptCondition supports `[gitge:2.40]`: true when git on PATH is at least that.
func ScriptCondition(cond string) (bool, error) {
	if v, ok := strings.CutPrefix(cond, "gitge:"); ok {
		return gitAtLeast(v)
	}
	return false, fmt.Errorf("unknown condition %q", cond)
}

func gitAtLeast(want string) (bool, error) {
	w, err := gitx.ParseVersion(want)
	if err != nil {
		return false, err
	}
	have, err := gitx.ParseVersion(GitVersion())
	if err != nil {
		return false, err
	}
	return !have.Less(w), nil
}

// atomicsave <file> <text>: write a temp file in the same directory, then
// rename over the target, the way an editor or agent saves. Git cannot see it.
func cmdAtomicSave(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: atomicsave file text")
	}
	path := ts.MkAbs(args[0])
	tmp, err := os.CreateTemp(filepath.Dir(path), ".atomic-*")
	ts.Check(err)
	_, err = tmp.WriteString(args[1])
	ts.Check(err)
	ts.Check(tmp.Close())
	ts.Check(os.Chmod(tmp.Name(), 0o644))
	ts.Check(os.Rename(tmp.Name(), path))
}

// islink <path> [target-suffix]: succeeds when path is a symlink (whose target
// ends in target-suffix). Use `! islink` to assert it is not.
func cmdIsLink(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) < 1 || len(args) > 2 {
		ts.Fatalf("usage: islink path [target-suffix]")
	}
	target, err := os.Readlink(ts.MkAbs(args[0]))
	isLink := err == nil
	if len(args) == 2 && isLink {
		isLink = strings.HasSuffix(filepath.ToSlash(target), args[1])
	}
	if isLink == neg {
		if neg {
			ts.Fatalf("%s is a symlink (-> %s), want not", args[0], target)
		}
		ts.Fatalf("%s is not a symlink to ...%s (target %q, err %v)", args[0], strings.Join(args[1:], ""), target, err)
	}
}

// samefile a b: a and b are one regular file under two names (a hard link); `! samefile a b` says
// they are not. A symlink is not "the same file" here: both must be regular files.
func cmdSameFile(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: samefile a b")
	}
	same := false
	ai, aerr := os.Lstat(ts.MkAbs(args[0]))
	bi, berr := os.Lstat(ts.MkAbs(args[1]))
	if aerr == nil && berr == nil && ai.Mode().IsRegular() && bi.Mode().IsRegular() {
		same = os.SameFile(ai, bi)
	}
	if same == neg {
		if neg {
			ts.Fatalf("%s and %s are one file (a hard link), want not", args[0], args[1])
		}
		ts.Fatalf("%s and %s are not one file (errors %v, %v)", args[0], args[1], aerr, berr)
	}
}

// blobsha <path>: print the git blob id of the file's current bytes.
func cmdBlobSHA(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: blobsha path")
	}
	b, err := os.ReadFile(ts.MkAbs(args[0]))
	ts.Check(err)
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	fmt.Fprintf(ts.Stdout(), "%x\n", h.Sum(nil))
}

// gitver >= X: fails unless git on PATH is at least X.
func cmdGitVer(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 2 || args[0] != ">=" {
		ts.Fatalf("usage: gitver >= X.Y")
	}
	ok, err := gitAtLeast(args[1])
	ts.Check(err)
	if ok == neg {
		ts.Fatalf("git %s: gitver >= %s is %v", GitVersion(), args[1], ok)
	}
}

// hooklog: print what the fake hooks logged (argv and stdin), for `stdout`.
func cmdHookLog(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 0 {
		ts.Fatalf("usage: hooklog")
	}
	b, _ := os.ReadFile(ts.Getenv(HookLogEnv))
	fmt.Fprint(ts.Stdout(), string(b))
}

// gitcalls reset | gitcalls <max> | gitcalls ==<n>: the budget gate. `reset`
// truncates the trace; `<max>` fails when more git processes ran since the last
// reset, `==<n>` when the count is not exactly n (proving the tracer works). A
// missing trace file is an error, never a pass.
func cmdGitCalls(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 1 {
		ts.Fatalf("usage: gitcalls reset | gitcalls <max>")
	}
	trace := ts.Getenv(gitx.TraceEnv)
	if args[0] == "reset" {
		ts.Check(os.WriteFile(trace, nil, 0o600))
		return
	}
	exact := strings.HasPrefix(args[0], "==")
	max, err := strconv.Atoi(strings.TrimPrefix(args[0], "=="))
	ts.Check(err)
	b, err := os.ReadFile(trace)
	if err != nil {
		ts.Fatalf("no git trace at %s (did you `gitcalls reset` first?): %v", trace, err)
	}
	n := strings.Count(string(b), "\n")
	if exact && n != max {
		ts.Fatalf("%d git calls, want exactly %d:\n%s", n, max, b)
	}
	if n > max {
		ts.Fatalf("%d git calls, budget is %d:\n%s", n, max, b)
	}
}

// fixture <dir> [team|plain] [husky] [lefthook] [hooks] [upstream] [conflict]
// [worktrees=N] [cone]: build a fixture repository (docs/HANDOVER.md §7.3) at
// <dir>, relative to the script's work dir. Linked worktrees land next to it as
// <dir>-wt1.., and the bare upstream as <dir>-upstream.git.
func cmdFixture(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 1 {
		ts.Fatalf("usage: fixture dir [team|plain] [husky] [lefthook] [hooks] [upstream] [conflict] [worktrees=N] [cone]")
	}
	dir := ts.MkAbs(args[0])
	recipe := Team
	var opts []Option
	for _, a := range args[1:] {
		switch {
		case a == "team":
			recipe = Team
		case a == "plain":
			recipe = Plain
		case a == "husky":
			opts = append(opts, WithHusky())
		case a == "lefthook":
			opts = append(opts, WithLefthook())
		case a == "hooks":
			opts = append(opts, WithHooks())
		case a == "upstream":
			opts = append(opts, WithUpstream())
		case a == "conflict":
			opts = append(opts, WithConflictBranch())
		case a == "cone":
			opts = append(opts, WithCone())
		case strings.HasPrefix(a, "worktrees="):
			n, err := strconv.Atoi(strings.TrimPrefix(a, "worktrees="))
			ts.Check(err)
			opts = append(opts, WithWorktrees(n))
		default:
			ts.Fatalf("fixture: unknown option %q", a)
		}
	}
	// The script's own environment, so the fixture and later commands agree.
	var env []string
	for _, k := range []string{"HOME", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_AUTHOR_DATE", "GIT_COMMITTER_DATE", "TZ", "LC_ALL", "GIT_TERMINAL_PROMPT", "PATH", HookLogEnv} {
		env = append(env, k+"="+ts.Getenv(k))
	}
	_, _, err := Materialize(dir, filepath.Dir(dir), scrub(append(os.Environ(), env...)), recipe, opts...)
	ts.Check(err)
}
