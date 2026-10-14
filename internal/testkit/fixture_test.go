package testkit_test

import (
	"os"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/testkit"
)

func TestMonorepoHistoryIsDeterministic(t *testing.T) {
	a := testkit.New(t, testkit.Monorepo)
	b := testkit.New(t, testkit.Monorepo)
	if a.Git("rev-parse", "HEAD") != b.Git("rev-parse", "HEAD") {
		t.Fatal("same recipe must give the same history")
	}
	if a.Git("status", "--porcelain") != "" {
		t.Fatal("fresh fixture must be clean")
	}
	if n := strings.Count(a.Git("log", "--format=%s"), "\n"); n != 1 {
		t.Fatalf("want 2 commits, got %d newlines", n)
	}
	if !strings.Contains(a.Git("ls-files", ".outfit"), ".outfit/profiles/yolo/AGENTS.md") {
		t.Fatal("Monorepo must commit .outfit/")
	}
}

func TestPlainHasNoOutfit(t *testing.T) {
	r := testkit.New(t, testkit.Plain)
	if r.Git("ls-files", ".outfit") != "" {
		t.Fatal("Plain must not carry .outfit/")
	}
}

func TestFixturesAreIndependent(t *testing.T) {
	a := testkit.New(t, testkit.Monorepo)
	b := testkit.New(t, testkit.Monorepo)
	a.Write("AGENTS.md", "changed\n")
	if b.Read("AGENTS.md") == "changed\n" {
		t.Fatal("copies must be private to a test")
	}
}

func TestHermeticIdentityAndDefaultBranch(t *testing.T) {
	r := testkit.New(t, testkit.Monorepo)
	if got := r.Git("var", "GIT_COMMITTER_IDENT"); !strings.HasPrefix(got, "Test Committer <committer@example.com> 1700000000 +0000") {
		t.Fatalf("ident = %q", got)
	}
	if r.Git("symbolic-ref", "--short", "HEAD") != "main" {
		t.Fatal("default branch must be main")
	}
}

func TestOptions(t *testing.T) {
	t.Run("upstream", func(t *testing.T) {
		r := testkit.New(t, testkit.Monorepo, testkit.WithUpstream())
		if r.Git("rev-parse", "HEAD") != r.Git("--git-dir="+r.Upstream, "rev-parse", "main") {
			t.Fatal("upstream must have main pushed")
		}
	})
	t.Run("conflict", func(t *testing.T) {
		r := testkit.New(t, testkit.Monorepo, testkit.WithConflictBranch())
		if _, err := r.GitErr("merge", "conflict"); err == nil {
			t.Fatal("merge must conflict")
		}
		if !strings.Contains(r.Read("AGENTS.md"), "<<<<<<<") {
			t.Fatal("AGENTS.md must hold conflict markers")
		}
	})
	t.Run("worktrees", func(t *testing.T) {
		r := testkit.New(t, testkit.Monorepo, testkit.WithWorktrees(2))
		if len(r.Worktrees) != 2 || strings.Count(r.Git("worktree", "list"), "\n") != 2 {
			t.Fatalf("worktrees: %v", r.Worktrees)
		}
	})
	t.Run("cone", func(t *testing.T) {
		r := testkit.New(t, testkit.Monorepo, testkit.WithCone())
		if r.Git("config", "core.sparseCheckoutCone") != "true" {
			t.Fatal("cone sparse checkout must be on")
		}
		if _, err := os.Stat(r.Path("apps/api/main.go")); err == nil {
			t.Fatal("apps/api must be outside the cone")
		}
	})
}

func TestHuskyLayoutRunsAndLogs(t *testing.T) {
	r := testkit.New(t, testkit.Monorepo, testkit.WithHusky())
	if r.Git("config", "core.hooksPath") != ".husky/_" {
		t.Fatal("husky sets core.hooksPath=.husky/_")
	}
	if r.Git("status", "--porcelain") != "" {
		t.Fatal(".husky/_ must be ignored")
	}
	r.Write("x.txt", "x\n")
	r.Git("add", "x.txt")
	r.Git("commit", "-qm", "feat: x")
	if !strings.Contains(r.Hooks(), "hook=pre-commit argv=") {
		t.Fatalf("pre-commit did not log: %q", r.Hooks())
	}
	// A failing hook blocks the commit and the exit code comes through.
	if err := os.WriteFile(r.HookLog+".exit.pre-commit", []byte("7"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.Write("y.txt", "y\n")
	r.Git("add", "y.txt")
	if _, err := r.GitErr("commit", "-qm", "feat: y"); err == nil {
		t.Fatal("a failing pre-commit must block the commit")
	}
}

func TestPlainAndLefthookStubs(t *testing.T) {
	r := testkit.New(t, testkit.Monorepo, testkit.WithHooks(), testkit.WithLefthook())
	r.Git("commit", "-q", "--allow-empty", "-m", "feat: empty")
	if !strings.Contains(r.Hooks(), "hook=pre-commit") && !strings.Contains(r.Hooks(), "hook=commit-msg") {
		t.Fatalf("hooks did not run: %q", r.Hooks())
	}
}
