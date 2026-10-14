package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

func TestParseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want gitx.Version
	}{
		{"git version 2.43.0", gitx.Version{2, 43, 0}},
		{"git version 2.39.5 (Apple Git-154)", gitx.Version{2, 39, 5}},
		{"git version 2.36.0.rc1", gitx.Version{2, 36, 0}},
		{"2.50", gitx.Version{2, 50, 0}},
		{"git version 2.54.0\n", gitx.Version{2, 54, 0}},
	}
	for _, c := range cases {
		got, err := gitx.ParseVersion(c.in)
		if err != nil || got != c.want {
			t.Errorf("gitx.ParseVersion(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if _, err := gitx.ParseVersion("no digits here"); err == nil {
		t.Error("expected an error for unparsable input")
	}
}

func TestCheckFloor(t *testing.T) {
	cases := []struct {
		v    gitx.Version
		fail bool
	}{
		{gitx.Version{2, 34, 1}, true}, // Ubuntu 22.04
		{gitx.Version{2, 35, 9}, true},
		{gitx.Version{2, 36, 0}, false},
		{gitx.Version{2, 39, 5}, false}, // Apple git
		{gitx.Version{3, 0, 0}, false},
	}
	for _, c := range cases {
		err := gitx.CheckFloor(c.v)
		if (err != nil) != c.fail {
			t.Errorf("gitx.CheckFloor(%v) = %v, want fail=%v", c.v, err, c.fail)
		}
	}
	var fe *gitx.FloorError
	err := gitx.CheckFloor(gitx.Version{2, 34, 1})
	if !errors.As(err, &fe) {
		t.Fatalf("want *gitx.FloorError, got %T", err)
	}
	if !strings.Contains(err.Error(), "ppa:git-core/ppa") || !strings.Contains(err.Error(), "2.34.1") {
		t.Errorf("error text must name the fix: %q", err.Error())
	}
}

func TestVersionLess(t *testing.T) {
	if !(gitx.Version{2, 9, 0}).Less(gitx.Version{2, 10, 0}) {
		t.Error("2.9 must be older than 2.10 (numeric, not lexical)")
	}
	if (gitx.Version{2, 36, 0}).Less(gitx.Version{2, 36, 0}) {
		t.Error("equal versions are not Less")
	}
}

func TestCallCounting(t *testing.T) {
	testkit.Isolate(t)
	gitx.ResetCalls()
	c := gitx.Client{}
	if _, err := c.Read(context.Background(), "--version"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write(context.Background(), "--version"); err != nil {
		t.Fatal(err)
	}
	if got := gitx.Calls(); got != 2 {
		t.Fatalf("gitx.Calls() = %d, want 2", got)
	}
}

func TestTraceFile(t *testing.T) {
	testkit.Isolate(t)
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv(gitx.TraceEnv, trace)
	if _, err := (gitx.Client{}).Read(context.Background(), "--version"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(trace)
	if err != nil || strings.Count(string(b), "\n") != 1 || !strings.Contains(string(b), "git --version") {
		t.Fatalf("trace = %q, err = %v", b, err)
	}
}

func TestReadSetsOptionalLocksOff(t *testing.T) {
	testkit.Isolate(t)
	// `git var` is cheap; GIT_OPTIONAL_LOCKS shows up in the environment of a
	// child, which we observe through an alias that prints it.
	res, err := gitx.Client{Env: []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=alias.lock", "GIT_CONFIG_VALUE_0=!printf %s \"$GIT_OPTIONAL_LOCKS\""}}.Read(context.Background(), "lock")
	if err != nil {
		t.Skipf("alias execution unavailable: %v", err)
	}
	if string(res.Stdout) != "0" {
		t.Fatalf("GIT_OPTIONAL_LOCKS = %q on a read, want 0", res.Stdout)
	}
	res, err = gitx.Client{Env: []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=alias.lock", "GIT_CONFIG_VALUE_0=!printf %s \"$GIT_OPTIONAL_LOCKS\""}}.Write(context.Background(), "lock")
	if err != nil {
		t.Fatal(err)
	}
	if string(res.Stdout) == "0" && os.Getenv("GIT_OPTIONAL_LOCKS") != "0" {
		t.Fatal("a write must not set GIT_OPTIONAL_LOCKS=0")
	}
}

func TestErrorCarriesExitCodeAndStderr(t *testing.T) {
	testkit.Isolate(t)
	res, err := gitx.Client{Dir: t.TempDir()}.Read(context.Background(), "rev-parse", "--git-dir")
	var ge *gitx.Error
	if !errors.As(err, &ge) || ge.ExitCode == 0 || res.ExitCode != ge.ExitCode {
		t.Fatalf("want *gitx.Error with non-zero exit, got %v", err)
	}
}

// fakeGit puts a `git` that prints out on PATH (and nothing else).
func fakeGit(t *testing.T, out string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho '" + out + "'\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestRequireFloorOnFakeGit(t *testing.T) {
	cases := []struct {
		out  string
		fail bool
	}{
		{"git version 2.34.1", true},
		{"git version 2.36.0", false},
		{"git version 2.39.5 (Apple Git-154)", false},
	}
	for _, c := range cases {
		fakeGit(t, c.out)
		_, err := gitx.Client{}.RequireFloor(context.Background())
		if (err != nil) != c.fail {
			t.Errorf("%q: err = %v, want fail=%v", c.out, err, c.fail)
		}
	}
}

// Row 18 of docs/git-behavior.md: `git config get` needs git 2.46+, the floor is
// 2.36. Nothing in the module may spell it.
func TestNoGitConfigGet(t *testing.T) {
	re := regexp.MustCompile(`"config",\s*"get"|git config get`)
	err := filepath.WalkDir("../..", func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "gitx_test.go") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if re.Match(b) {
			t.Errorf("%s uses `git config get` (needs git 2.46+)", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Row 4 of docs/git-behavior.md: `git ls-files -s -t` is one call that gives the
// skip-worktree tag, the mode, the blob and the stage. Pins the FORMAT on every
// git in the CI matrix, including the 2.36 floor.
func TestLsFilesSkipWorktreeFormat(t *testing.T) {
	r := testkit.New(t, testkit.Monorepo)
	r.Git("update-index", "--skip-worktree", "AGENTS.md")
	res, err := gitx.Client{Dir: r.Dir, Env: r.Env}.Read(context.Background(), "ls-files", "-s", "-t", "--", "AGENTS.md", "README.md")
	if err != nil {
		t.Fatal(err)
	}
	sha := r.Git("rev-parse", "HEAD:AGENTS.md")
	want := "S 100644 " + sha + " 0\tAGENTS.md\n"
	if !strings.HasPrefix(string(res.Stdout), want) {
		t.Fatalf("got %q, want prefix %q", res.Stdout, want)
	}
	if !strings.Contains(string(res.Stdout), "H 100644 ") {
		t.Fatalf("a normal file must be tagged H: %q", res.Stdout)
	}
}
