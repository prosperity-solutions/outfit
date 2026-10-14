package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/hooks"
	"github.com/prosperity-solutions/outfit/internal/state"
	"github.com/prosperity-solutions/outfit/internal/testkit"
)

// guardFixture prepares a repo whose AGENTS.md is "swapped": a state file that
// says so and the sparse-checkout file outfit would manage.
func guardFixture(t *testing.T, opts ...testkit.Option) (*testkit.Repo, *state.State, config.GitConfig, gitx.Client) {
	t.Helper()
	f := testkit.New(t, testkit.Team, opts...)
	gitDir := filepath.Join(f.Dir, ".git")
	sparse := state.SparseMarker + "\n/*\n!/AGENTS.md\n"
	if err := os.MkdirAll(filepath.Join(gitDir, "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "info", "sparse-checkout"), []byte(sparse), 0o644); err != nil {
		t.Fatal(err)
	}
	f.Git("config", "core.sparseCheckout", "true")
	st := &state.State{Swaps: []state.Swap{{Path: "AGENTS.md"}}}
	return f, st, loadCfg(t, f), gitx.Client{Dir: f.Dir, Env: f.Env}
}

func loadCfg(t *testing.T, f *testkit.Repo) config.GitConfig {
	t.Helper()
	out := f.Git("config", "--list", "--show-scope", "-z")
	return config.ParseGitList([]byte(out))
}

func guard(t *testing.T, f *testkit.Repo, st *state.State, env map[string]string) []hooks.Violation {
	t.Helper()
	gc := loadCfg(t, f)
	vs, err := hooks.Guard(context.Background(), gitx.Client{Dir: f.Dir, Env: f.Env}, st, filepath.Join(f.Dir, ".git"), gc,
		func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return vs
}

func TestGuardIsFreeWithNothingSwapped(t *testing.T) {
	f := testkit.New(t, testkit.Team)
	f.Write("AGENTS.md", "edited\n")
	f.Git("add", "--sparse", "AGENTS.md")
	gitx.ResetCalls()
	if vs := guard(t, f, &state.State{}, nil); len(vs) != 0 {
		t.Fatalf("base must not be guarded: %v", vs)
	}
	if vs := guard(t, f, nil, nil); len(vs) != 0 {
		t.Fatalf("no state, no guard: %v", vs)
	}
}

func TestGuardRefusesAStagedSwappedPath(t *testing.T) {
	f, st, _, _ := guardFixture(t)
	f.Write("AGENTS.md", "edited\n")
	f.Git("add", "--sparse", "AGENTS.md")
	vs := guard(t, f, st, nil)
	if len(vs) != 1 || vs[0].Path != "AGENTS.md" || !strings.Contains(vs[0].Fix, "git reset -q -- AGENTS.md") {
		t.Fatalf("got %+v", vs)
	}
	// OUTFIT_ALLOW=1 lets a deliberate commit through...
	if vs := guard(t, f, st, map[string]string{"OUTFIT_ALLOW": "1"}); len(vs) != 0 {
		t.Fatalf("OUTFIT_ALLOW=1: %+v", vs)
	}
	// ...but a path outside the swapped set is never the guard's business.
	f.Git("reset", "-q")
	f.Write("README.md", "x\n")
	f.Git("add", "README.md")
	if vs := guard(t, f, st, nil); len(vs) != 0 {
		t.Fatalf("unrelated path: %+v", vs)
	}
}

// The verified disaster: the link itself must never reach history, whatever
// OUTFIT_ALLOW says.
func TestGuardAlwaysRefusesASymlinkBlob(t *testing.T) {
	f, st, _, _ := guardFixture(t)
	if err := os.Remove(f.Path("AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".outfit/profiles/yolo/AGENTS.md", f.Path("AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	f.Git("add", "--sparse", "AGENTS.md")
	for _, env := range []map[string]string{nil, {"OUTFIT_ALLOW": "1"}} {
		vs := guard(t, f, st, env)
		if len(vs) != 1 || !strings.Contains(vs[0].Message, "mode 120000") {
			t.Fatalf("env %v: %+v", env, vs)
		}
	}
}

func TestGuardAllowsResolvingAMergeConflictWithARegularFile(t *testing.T) {
	f, st, _, _ := guardFixture(t, testkit.WithConflictBranch())
	if _, err := f.GitErr("merge", "conflict"); err == nil {
		t.Fatal("merge must conflict")
	}
	f.Write("AGENTS.md", "# Agents\nresolved\n")
	f.Git("add", "--sparse", "AGENTS.md")
	if vs := guard(t, f, st, nil); len(vs) != 0 {
		t.Fatalf("a regular-file resolution during a merge is legitimate: %+v", vs)
	}
	// Even during a merge, a symlink blob is not.
	_ = os.Remove(f.Path("AGENTS.md"))
	if err := os.Symlink("elsewhere", f.Path("AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	f.Git("add", "--sparse", "AGENTS.md")
	if vs := guard(t, f, st, nil); len(vs) != 1 {
		t.Fatalf("%+v", vs)
	}
}

// `git sparse-checkout disable` followed by `commit -a` committed the symlink
// (verified, docs/git-behavior.md row 13): a missing sparse config is itself a
// refusal.
func TestGuardRefusesWhenTheSparseConfigIsMissing(t *testing.T) {
	f, st, _, _ := guardFixture(t)
	f.Git("config", "core.sparseCheckout", "false")
	vs := guard(t, f, st, nil)
	if len(vs) != 1 || !strings.Contains(vs[0].Message, "sparse checkout is off") || vs[0].Fix != "outfit sync" {
		t.Fatalf("%+v", vs)
	}
	f.Git("config", "core.sparseCheckout", "true")
	if err := os.Remove(filepath.Join(f.Dir, ".git", "info", "sparse-checkout")); err != nil {
		t.Fatal(err)
	}
	if vs := guard(t, f, st, nil); len(vs) != 1 || !strings.Contains(vs[0].Message, "missing") {
		t.Fatalf("%+v", vs)
	}
	// A foreign sparse file (no marker) and one that stopped hiding the path.
	for body, want := range map[string]string{"/*\n": "not outfit-managed", state.SparseMarker + "\n/*\n": "no longer hides AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(f.Dir, ".git", "info", "sparse-checkout"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if vs := guard(t, f, st, nil); len(vs) != 1 || !strings.Contains(vs[0].Message, want) {
			t.Fatalf("%q: %+v", body, vs)
		}
	}
}
