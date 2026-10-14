package testkit

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Recipe is the shape of the fixture repository.
type Recipe int

const (
	// Monorepo is a small monorepo with a committed .outfit/ (config plus the
	// yolo and lean profiles).
	Monorepo Recipe = iota
	// Plain is the same monorepo without any .outfit/ directory: a repo that has
	// never heard of outfit.
	Plain
)

func (r Recipe) String() string {
	if r == Plain {
		return "plain"
	}
	return "monorepo"
}

type spec struct {
	recipe    Recipe
	husky     bool
	lefthook  bool
	plain     bool // plain .git/hooks stubs
	upstream  bool
	conflict  bool
	worktrees int
	cone      bool
}

// Option customises a fixture.
type Option func(*spec)

// WithHusky adds a husky v9 layout: .husky/<hook> committed, .husky/_ generated
// and ignored, core.hooksPath=.husky/_ in the local config.
func WithHusky() Option { return func(s *spec) { s.husky = true } }

// WithLefthook adds lefthook.yml and stubs in .git/hooks that log `via=lefthook`
// (lefthook installs its shims there). It replaces WithHooks' plain stubs.
func WithLefthook() Option { return func(s *spec) { s.lefthook = true } }

// WithHooks adds plain logging stubs in .git/hooks.
func WithHooks() Option { return func(s *spec) { s.plain = true } }

// WithUpstream gives the clone a bare "origin" with main pushed.
func WithUpstream() Option { return func(s *spec) { s.upstream = true } }

// WithConflictBranch creates branch `conflict` and a commit on main so that
// `git merge conflict` conflicts on AGENTS.md.
func WithConflictBranch() Option { return func(s *spec) { s.conflict = true } }

// WithWorktrees adds n linked worktrees, wt1..wtn, each on its own branch.
func WithWorktrees(n int) Option { return func(s *spec) { s.worktrees = n } }

// WithCone turns on a foreign cone-mode sparse checkout (apps/web only).
func WithCone() Option { return func(s *spec) { s.cone = true } }

// Repo is one fixture instance, private to a test.
type Repo struct {
	T         *testing.T
	Dir       string   // the clone's working tree
	Home      string   // hermetic $HOME
	Env       []string // hermetic environment
	Upstream  string   // bare origin ("" without WithUpstream)
	Worktrees []string // linked worktree paths
	HookLog   string   // file the fake hooks log to
}

// SchemaVersion invalidates cached fixtures when the recipe code changes.
const SchemaVersion = "1"

// New builds a fixture for t. History is one `git fast-import` stream, cached
// per (recipe, committed-file options, git version) and copied per test.
func New(t *testing.T, recipe Recipe, opts ...Option) *Repo {
	t.Helper()
	SkipIfShort(t)
	s := spec{recipe: recipe}
	for _, o := range opts {
		o(&s)
	}

	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := WriteHome(home); err != nil {
		t.Fatal(err)
	}
	r := &Repo{T: t, Home: home, Dir: filepath.Join(root, "repo"), HookLog: filepath.Join(root, "hooks.log")}
	r.Env = HermeticEnviron(home)
	r.Env = append(r.Env, HookLogEnv+"="+r.HookLog)

	cache := cachedFixture(t, s, r.Env)
	if err := copyTree(cache, r.Dir); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	// Copying changed every mtime: refresh the index so git trusts its stat cache
	// (otherwise it re-hashes lazily and a later sparse change can skip files it
	// believes are modified).
	r.Git("update-index", "-q", "--refresh")
	// Local state that holds absolute paths cannot live in the cache.
	if s.upstream {
		r.Upstream = filepath.Join(root, "upstream.git")
		r.Git("init", "-q", "--bare", r.Upstream)
		r.Git("remote", "add", "origin", r.Upstream)
		r.Git("push", "-q", "-u", "origin", "main")
	}
	if s.conflict {
		r.Git("branch", "conflict", "HEAD~1")
		r.Git("checkout", "-q", "conflict")
		r.Write("AGENTS.md", "# Agents\nconflict side\n")
		r.Git("commit", "-qam", "conflict side")
		r.Git("checkout", "-q", "main")
		r.Write("AGENTS.md", "# Agents\nmain side\n")
		r.Git("commit", "-qam", "main side")
	}
	for i := 1; i <= s.worktrees; i++ {
		p := filepath.Join(root, fmt.Sprintf("wt%d", i))
		r.Git("worktree", "add", "-q", "-b", fmt.Sprintf("wt%d", i), p)
		r.Worktrees = append(r.Worktrees, p)
	}
	if s.cone {
		r.Git("sparse-checkout", "init", "--cone")
		r.Git("sparse-checkout", "set", "apps/web")
	}
	return r
}

// SkipIfShort skips integration and e2e tests under `go test -short`, which is
// what the unit job runs.
func SkipIfShort(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: skipped with -short")
	}
}

// Git runs git in the repo and fails the test on error. It returns trimmed stdout.
func (r *Repo) Git(args ...string) string {
	r.T.Helper()
	out, err := r.GitErr(args...)
	if err != nil {
		r.T.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// GitErr is Git without the failure.
func (r *Repo) GitErr(args ...string) (string, error) {
	return RunGit(r.Dir, r.Env, args...)
}

// GitIn runs git in another directory (a worktree) with the same environment.
func (r *Repo) GitIn(dir string, args ...string) string {
	r.T.Helper()
	out, err := RunGit(dir, r.Env, args...)
	if err != nil {
		r.T.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return out
}

// Path joins p onto the working tree.
func (r *Repo) Path(p string) string { return filepath.Join(r.Dir, filepath.FromSlash(p)) }

// Write writes a file (creating parents).
func (r *Repo) Write(p, content string) {
	r.T.Helper()
	full := r.Path(p)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.T.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		r.T.Fatal(err)
	}
}

// Read returns a file's content.
func (r *Repo) Read(p string) string {
	r.T.Helper()
	b, err := os.ReadFile(r.Path(p))
	if err != nil {
		r.T.Fatal(err)
	}
	return string(b)
}

// Hooks returns what the fake hooks logged so far.
func (r *Repo) Hooks() string {
	b, _ := os.ReadFile(r.HookLog)
	return string(b)
}

// RunGit runs git with an explicit environment and returns trimmed combined output.
func RunGit(dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// GitVersion returns `git --version` from PATH.
func GitVersion() string {
	out, err := exec.Command("git", "--version").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// --- cache -----------------------------------------------------------------

func cachedFixture(t *testing.T, s spec, env []string) string {
	t.Helper()
	// The key covers the fixture's CONTENT (the fast-import stream and the hook
	// scripts), so editing the recipe invalidates stale caches on its own.
	h := sha256.New()
	fmt.Fprintf(h, "%s|%v|%v|%v|%v|%s|", SchemaVersion, s.recipe, s.husky, s.lefthook, s.plain, GitVersion())
	h.Write(fastImport(monorepoFiles(s)))
	fmt.Fprint(h, huskyShim, huskyH, hookStub("x"), stubVia("x", "lefthook"))
	key := hex.EncodeToString(h.Sum(nil))[:16]
	base := filepath.Join(os.TempDir(), fmt.Sprintf("outfit-testkit-%d", os.Getuid()))
	final := filepath.Join(base, key)
	if _, err := os.Stat(final); err == nil {
		// The cache lives under the OS temp dir, which gets cleaned piecemeal.
		// A directory that no longer holds a readable history is rebuilt.
		if _, verr := RunGit(final, env, "rev-parse", "--verify", "-q", "HEAD~1^{tree}"); verr == nil {
			return final
		}
		os.RemoveAll(final)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	tmp, err := os.MkdirTemp(base, "build-")
	if err != nil {
		t.Fatal(err)
	}
	if err := buildFixture(tmp, s, env); err != nil {
		os.RemoveAll(tmp)
		t.Fatalf("build fixture: %v", err)
	}
	// Atomic publish; if another process won the race, use its copy.
	if err := os.Rename(tmp, final); err != nil {
		os.RemoveAll(tmp)
		if _, serr := os.Stat(final); serr != nil {
			t.Fatalf("publish fixture: %v", err)
		}
	}
	return final
}

func buildFixture(dir string, s spec, env []string) error {
	// The cache is built with its own throwaway home, so it never depends on the
	// test that happened to build it.
	home, err := os.MkdirTemp("", "outfit-fixture-home-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(home)
	if err := WriteHome(home); err != nil {
		return err
	}
	benv := HermeticEnviron(home)
	if out, err := RunGit(dir, benv, "init", "-q", "-b", "main"); err != nil {
		return fmt.Errorf("init: %v: %s", err, out)
	}
	files := monorepoFiles(s)
	stream := fastImport(files)
	cmd := exec.Command("git", "fast-import", "--quiet")
	cmd.Dir, cmd.Env = dir, benv
	cmd.Stdin = bytes.NewReader(stream)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("fast-import: %v: %s", err, out)
	}
	if out, err := RunGit(dir, benv, "reset", "-q", "--hard", "main"); err != nil {
		return fmt.Errorf("reset: %v: %s", err, out)
	}
	if s.husky {
		for _, h := range hookNames {
			if err := writeFile(filepath.Join(dir, ".husky", "_", h), huskyShim, 0o755); err != nil {
				return err
			}
		}
		if err := writeFile(filepath.Join(dir, ".husky", "_", "h"), huskyH, 0o755); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, ".husky", "_", ".gitignore"), "*\n", 0o644); err != nil {
			return err
		}
		if out, err := RunGit(dir, benv, "config", "core.hooksPath", ".husky/_"); err != nil {
			return fmt.Errorf("hooksPath: %v: %s", err, out)
		}
	}
	if s.plain || s.lefthook {
		via := ""
		if s.lefthook {
			via = "lefthook"
		}
		for _, h := range hookNames {
			if err := writeFile(filepath.Join(dir, ".git", "hooks", h), stubVia(h, via), 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

var hookNames = []string{"pre-commit", "commit-msg", "pre-push", "post-checkout", "post-merge", "post-rewrite"}

// hookStub is a fake team hook: it logs argv and stdin to $OUTFIT_TEST_HOOKLOG
// and exits with the code found in $OUTFIT_TEST_HOOKLOG.exit.<name>, else 0.
func hookStub(name string) string { return stubVia(name, "") }

// stubVia is hookStub that also records which hook manager it pretends to be.
func stubVia(name, via string) string {
	return fmt.Sprintf(`#!/bin/sh
# fake %[1]s hook (outfit testkit)
name=%[1]s
log="${OUTFIT_TEST_HOOKLOG:-/dev/null}"
{
  printf 'hook=%%s' "$name"
  [ -z %[2]q ] || printf ' via=%%s' %[2]q
  printf ' argv='
  for a in "$@"; do printf '[%%s]' "$a"; done
  printf '\n'
  case "$name" in
    pre-push|post-rewrite) printf 'stdin=<<\n'; cat; printf '>>\n' ;;
  esac
} >> "$log"
if [ -f "$log.exit.$name" ]; then exit "$(cat "$log.exit.$name")"; fi
exit 0
`, name, via)
}

const huskyShim = `#!/usr/bin/env sh
. "$(dirname "$0")/h"
`

// huskyH mirrors husky v9's .husky/_/h dispatcher.
const huskyH = `#!/usr/bin/env sh
[ "$HUSKY" = "2" ] && set -x
n=$(basename "$0")
s=$(dirname "$(dirname "$0")")/$n

[ ! -f "$s" ] && exit 0

sh -e "$s" "$@"
c=$?

[ $c != 0 ] && echo "husky - $n script failed (code $c)"
[ $c = 127 ] && echo "husky - command not found in PATH=$PATH"
exit $c
`

func writeFile(path, content string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), mode)
}

type file struct {
	path, content string
	exec          bool
}

func monorepoFiles(s spec) []file {
	fs := []file{
		{path: "README.md", content: "# Monorepo\n"},
		{path: "AGENTS.md", content: "# Agents\nbase rules\n"},
		{path: "CLAUDE.md", content: "# Claude\nbase rules\n"},
		{path: "apps/web/AGENTS.md", content: "# Web agents\nbase web rules\n"},
		{path: "apps/web/package.json", content: "{\"name\":\"web\"}\n"},
		{path: "apps/web/src/index.ts", content: "export const web = 1\n"},
		{path: "apps/api/README.md", content: "# api\n"},
		{path: "apps/api/main.go", content: "package main\n\nfunc main() {}\n"},
		{path: "packages/ui/index.ts", content: "export const ui = 1\n"},
		{path: "docs/guide.md", content: "# Guide\n"},
	}
	if s.recipe == Monorepo {
		fs = append(fs,
			file{path: ".outfit/config", content: "[outfit]\n\tlocked = secrets-scan, hook.pre-push\n\tsecrets-scan = on\n\tbiome = on\n"},
			file{path: ".outfit/profiles/yolo/profile.config", content: "[outfit]\n\tbiome = off\n\ttests = off\n\thook.pre-commit = off\n"},
			file{path: ".outfit/profiles/yolo/AGENTS.md", content: "# Agents\nyolo rules\n"},
			file{path: ".outfit/profiles/yolo/CLAUDE.md", content: "# Claude\nyolo rules\n"},
			file{path: ".outfit/profiles/lean/profile.config", content: "[outfit]\n\tbiome = off\n"},
			file{path: ".outfit/profiles/lean/apps/web/AGENTS.md", content: "# Web agents\nlean web rules\n"},
		)
	}
	if s.husky {
		for _, h := range hookNames {
			fs = append(fs, file{path: ".husky/" + h, content: hookStub(h), exec: true})
		}
		fs = append(fs, file{path: ".gitignore", content: ".husky/_\n"})
	}
	if s.lefthook {
		fs = append(fs, file{path: "lefthook.yml", content: "pre-commit:\n  commands:\n    biome:\n      run: echo biome\n"})
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].path < fs[j].path })
	return fs
}

// fastImport renders two commits: the files, then a docs tweak, so HEAD~1
// exists for branch fixtures. Dates are fixed so object ids are stable.
func fastImport(files []file) []byte {
	var b bytes.Buffer
	commit := func(msg string, ts int, from string, fs []file) {
		fmt.Fprintf(&b, "commit refs/heads/main\ncommitter Test Committer <committer@example.com> %d +0000\n", ts)
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		if from != "" {
			fmt.Fprintf(&b, "from %s\n", from)
		}
		for _, f := range fs {
			mode := "100644"
			if f.exec {
				mode = "100755"
			}
			fmt.Fprintf(&b, "M %s inline %s\ndata %d\n%s\n", mode, f.path, len(f.content), f.content)
		}
	}
	commit("feat: initial monorepo", 1700000000, "", files)
	commit("docs: tweak the guide", 1700000100, "",
		[]file{{path: "docs/guide.md", content: "# Guide\n\nUpdated.\n"}})
	return b.Bytes()
}

// copyTree copies src to dst preserving modes and symlinks.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			l, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(l, target)
		default:
			in, err := os.Open(p)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				out.Close()
				return err
			}
			return out.Close()
		}
	})
}
