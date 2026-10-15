package e2e

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"pgregory.net/rapid"

	"github.com/prosperity-solutions/outfit/internal/testkit"
)

// Model-based property test (HANDOVER.md §7.5): random sequences of what people and git
// do to a worktree that outfit manages, with the invariants checked after every step:
//
//   - no user byte is ever lost
//   - no swapped path is ever staged, and no symlink is ever staged or committed
//   - a clean status stays clean after the commands that must not dirty it
//   - sync, run twice, changes nothing the second time (no write, no mtime)
//   - no command fails with an internal error (exit 1): refusals are exit 3, "out of
//     sync" or "stale" are exit 4
//
// OUTFIT_PROPERTY_CHECKS (default 8) sets how many sequences run; rapid's own flags
// (-rapid.checks, -rapid.seed for a failing run's seed) work too.

type model struct {
	t      *testing.T
	rt     *rapid.T
	bin    string
	r      *testkit.Repo
	tokens []string
	other  string // a second clone that pushes to the upstream
	wts    []string
	nWT    int
	nProf  int
	locals []string
	steps  []string
	// pending: a user edit exists that sync has not absorbed or the tree has not shown.
	pending bool
}

// pendingAbsorb reports a regular file where a recorded link should be (an editor's save sync
// has not absorbed yet): the tree looks clean, but the next sync will change a profile file.
func (m *model) pendingAbsorb() bool {
	for _, p := range m.swapped() {
		if fi, err := os.Lstat(m.r.Path(p)); err == nil && fi.Mode()&os.ModeSymlink == 0 {
			return true
		}
	}
	return false
}

func (m *model) logf(format string, a ...any) {
	m.steps = append(m.steps, fmt.Sprintf(format, a...))
	m.rt.Logf(format, a...)
}

func (m *model) outfit(args ...string) testkit.Outcome {
	m.t.Helper()
	o := m.r.RunOutfit(m.bin, nil, args...)
	m.logf("outfit %s -> exit %d", strings.Join(args, " "), o.Code)
	if o.Code == 1 || o.Killed {
		m.rt.Fatalf("outfit %s failed with an internal error (exit %d)\nstdout: %s\nstderr: %s", strings.Join(args, " "), o.Code, o.Stdout, o.Stderr)
	}
	return o
}

func (m *model) git(args ...string) (string, error) {
	out, err := m.r.GitErr(args...)
	m.logf("git %s -> err=%v", strings.Join(args, " "), err)
	return out, err
}

func (m *model) swapped() []string {
	b, err := os.ReadFile(filepath.Join(m.r.Dir, ".git", "outfit", "state.json"))
	if err != nil {
		return nil
	}
	var st struct {
		Swaps []struct{ Path string } `json:"swaps"`
	}
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	var out []string
	for _, s := range st.Swaps {
		out = append(out, s.Path)
	}
	sort.Strings(out)
	return out
}

func (m *model) statusClean() bool {
	out, err := m.r.GitErr("status", "--short")
	return err == nil && strings.TrimSpace(out) == ""
}

// ---------------- the steps ----------------

func (m *model) pickSwapped() (string, bool) {
	sw := m.swapped()
	if len(sw) == 0 {
		return "", false
	}
	return sw[rapid.IntRange(0, len(sw)-1).Draw(m.rt, "path")], true
}

func (m *model) newToken() string {
	m.tokens = append(m.tokens, fmt.Sprintf("token-%d-%s", len(m.tokens)+1, strings.Repeat("x", 3)))
	return m.tokens[len(m.tokens)-1]
}

func (m *model) names() []string {
	return append([]string{"base", "yolo", "lean"}, m.locals...)
}

type step struct {
	name string
	// clean says the step must leave a clean status clean.
	clean bool
	run   func(m *model)
}

func steps() []step {
	return []step{
		{"use", true, func(m *model) {
			name := rapid.SampledFrom(m.names()).Draw(m.rt, "profile")
			args := []string{"use", name}
			if rapid.Bool().Draw(m.rt, "here") {
				args = append(args, "--here")
			}
			if o := m.outfit(args...); o.Code != 0 && o.Code != 3 {
				m.rt.Fatalf("use: %+v", o)
			}
		}},
		{"set", true, func(m *model) {
			key := rapid.SampledFrom([]string{"biome", "tests", "hook.pre-commit"}).Draw(m.rt, "key")
			val := rapid.SampledFrom([]string{"on", "off", ""}).Draw(m.rt, "val")
			if o := m.outfit("set", key+"="+val); o.Code != 0 && o.Code != 3 {
				m.rt.Fatalf("set: %+v", o)
			}
		}},
		{"new", true, func(m *model) {
			m.nProf++
			name := "p" + strconv.Itoa(m.nProf)
			args := []string{"new", name}
			if rapid.Bool().Draw(m.rt, "from") {
				args = append(args, "--from", rapid.SampledFrom([]string{"yolo", "lean"}).Draw(m.rt, "src"))
			}
			here := rapid.Bool().Draw(m.rt, "here")
			if here {
				args = append(args, "--here")
			}
			if o := m.outfit(args...); o.Code != 0 {
				m.rt.Fatalf("new: %+v", o)
			}
			if !here {
				m.locals = append(m.locals, name)
			}
		}},
		{"edit-through-the-link", false, func(m *model) {
			p, ok := m.pickSwapped()
			if !ok {
				m.logf("skipped: nothing applies")
				return
			}
			f, err := os.OpenFile(m.r.Path(p), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				m.logf("skipped: the file is not there")
				return
			}
			_, _ = f.WriteString(m.newToken() + "\n")
			_ = f.Close()
			m.logf("edit %s through the link", p)
		}},
		{"editor-atomic-save", false, func(m *model) {
			p, ok := m.pickSwapped()
			if !ok {
				m.logf("skipped: nothing applies")
				return
			}
			old, err := os.ReadFile(m.r.Path(p))
			if err != nil {
				m.logf("skipped: nothing applies")
				return
			}
			tmp := m.r.Path(p) + ".editor"
			if err := os.WriteFile(tmp, append(old, []byte(m.newToken()+"\n")...), 0o644); err != nil {
				m.logf("skipped: nothing applies")
				return
			}
			if err := os.Rename(tmp, m.r.Path(p)); err != nil {
				m.logf("skipped: nothing applies")
				return
			}
			m.logf("atomic save of %s", p)
		}},
		{"sync", true, func(m *model) {
			if o := m.outfit("sync"); o.Code != 0 && o.Code != 3 {
				m.rt.Fatalf("sync: %+v", o)
			}
		}},
		{"pull-docs", true, func(m *model) { m.upstreamChange("docs/guide.md", true) }},
		{"pull-base", false, func(m *model) { m.upstreamChange("AGENTS.md", true) }},
		{"merge", false, func(m *model) {
			_, err := m.git("merge", "-q", "--no-edit", "conflict")
			if err != nil {
				m.check("mid-merge")
				if _, aerr := m.git("merge", "--abort"); aerr != nil {
					m.logf("merge --abort: %v", aerr)
				}
			}
		}},
		{"worktree-add", true, func(m *model) {
			if len(m.wts) >= 2 {
				m.logf("skipped: nothing applies")
				return
			}
			m.nWT++
			p := filepath.Join(filepath.Dir(m.r.Dir), "pwt"+strconv.Itoa(m.nWT))
			if _, err := m.git("worktree", "add", "-q", "-b", "pwt"+strconv.Itoa(m.nWT), p); err == nil {
				m.wts = append(m.wts, p)
			}
		}},
		{"worktree-remove", true, func(m *model) {
			if len(m.wts) == 0 {
				m.logf("skipped: nothing applies")
				return
			}
			p := m.wts[len(m.wts)-1]
			if _, err := m.git("worktree", "remove", "--force", p); err == nil {
				m.wts = m.wts[:len(m.wts)-1]
			}
		}},
		{"stash", false, func(m *model) { _, _ = m.git("stash", "push", "-u", "-q") }},
		{"stash-pop", false, func(m *model) { _, _ = m.git("stash", "pop", "-q") }},
		{"rebase", false, func(m *model) {
			if _, err := m.git("commit", "--allow-empty", "-qm", "chore: empty"); err != nil {
				return
			}
			// --force-rebase: without it git sees an unchanged todo list and does nothing.
			if _, err := m.git("-c", "core.editor=true", "-c", "sequence.editor=true", "rebase", "-i", "--force-rebase", "HEAD~1"); err != nil {
				_, _ = m.git("rebase", "--abort")
			}
		}},
		{"commit-everything", false, func(m *model) {
			_, _ = m.git("add", "-A")
			_, _ = m.git("commit", "-qm", "feat: everything")
		}},
	}
}

// upstreamChange pushes a commit from a second clone and pulls it (fast-forward only).
func (m *model) upstreamChange(file string, pull bool) {
	if m.other == "" {
		m.other = filepath.Join(filepath.Dir(m.r.Dir), "other")
		if out, err := testkit.RunGit(filepath.Dir(m.r.Dir), m.r.Env, "clone", "-q", m.r.Upstream, m.other); err != nil {
			m.rt.Fatalf("clone: %v %s", err, out)
		}
	}
	if _, err := testkit.RunGit(m.other, m.r.Env, "pull", "-q", "--ff-only", "origin", "main"); err != nil {
		m.logf("skipped: nothing applies")
		return
	}
	b, _ := os.ReadFile(filepath.Join(m.other, file))
	if err := os.WriteFile(filepath.Join(m.other, file), append(b, []byte("upstream "+strconv.Itoa(len(m.steps))+"\n")...), 0o644); err != nil {
		m.logf("skipped: nothing applies")
		return
	}
	for _, args := range [][]string{{"commit", "-qam", "docs: upstream"}, {"push", "-q", "origin", "HEAD:main"}} {
		if _, err := testkit.RunGit(m.other, m.r.Env, args...); err != nil {
			m.logf("skipped: nothing applies")
			return
		}
	}
	if pull {
		// A local commit (the rebase step, commit-everything) makes a fast-forward
		// impossible: then rebase onto the upstream, like `git pull --rebase`.
		if _, err := m.git("pull", "-q", "--ff-only", "origin", "main"); err != nil {
			if _, rerr := m.git("pull", "-q", "--rebase", "origin", "main"); rerr != nil {
				_, _ = m.git("rebase", "--abort")
			}
		}
	}
}

// ---------------- the invariants ----------------

func (m *model) check(after string) {
	m.t.Helper()
	// An internal error is never acceptable; "out of sync" (4) and "needs a person" (3) are.
	if o := m.r.RunOutfit(m.bin, nil, "status", "--json"); o.Code == 1 || o.Killed {
		m.rt.Fatalf("after %s: status failed: %+v", after, o)
	}
	// No symlink in history, none staged.
	if out, _ := m.r.GitErr("ls-tree", "-r", "HEAD"); strings.Contains(out, "120000 ") {
		m.rt.Fatalf("after %s: a symlink is committed:\n%s", after, out)
	}
	if out, _ := m.r.GitErr("diff", "--cached", "--raw"); strings.Contains(out, " 120000 ") {
		m.rt.Fatalf("after %s: a symlink is staged:\n%s", after, out)
	}
	// No swapped path staged (paths in the middle of a merge are unmerged, not staged by anyone).
	unmerged := map[string]bool{}
	if out, _ := m.r.GitErr("ls-files", "-u"); out != "" {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if _, p, ok := strings.Cut(l, "\t"); ok {
				unmerged[p] = true
			}
		}
	}
	staged, _ := m.r.GitErr("diff", "--cached", "--name-only")
	for _, p := range m.swapped() {
		if unmerged[p] {
			continue
		}
		for _, s := range strings.Split(staged, "\n") {
			if s == p {
				m.rt.Fatalf("after %s: the swapped path %s is staged", after, p)
			}
		}
	}
	// No user byte lost.
	for _, tok := range m.tokens {
		if !m.tokenFound(tok) {
			m.rt.Fatalf("after %s: user bytes %q are gone\nsteps:\n%s", after, tok, strings.Join(m.steps, "\n"))
		}
	}
}

func (m *model) tokenFound(tok string) bool {
	roots := []string{m.r.Dir, filepath.Join(m.r.Dir, ".git", "outfit")}
	roots = append(roots, m.wts...)
	for _, root := range roots {
		found := false
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if d.IsDir() && d.Name() == ".git" && root != filepath.Join(m.r.Dir, ".git", "outfit") {
				return filepath.SkipDir
			}
			if d.Type().IsRegular() {
				if b, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(b), tok) {
					found = true
				}
			}
			return nil
		})
		if found {
			return true
		}
	}
	// Git may hold it: a stash, a commit.
	for _, args := range [][]string{{"stash", "list", "-p"}, {"log", "--all", "-p"}} {
		if out, err := m.r.GitErr(args...); err == nil && strings.Contains(out, tok) {
			return true
		}
	}
	return false
}

// mtimes records what a sync could touch.
func (m *model) mtimes() map[string]string {
	out := map[string]string{}
	add := func(p string) {
		if fi, err := os.Lstat(p); err == nil {
			out[p] = fi.ModTime().String() + fi.Mode().String()
		}
	}
	for _, p := range m.swapped() {
		add(m.r.Path(p))
	}
	for _, rel := range []string{".git/outfit/state.json", ".git/info/sparse-checkout", ".git/info/exclude", ".git/config", ".git/config.worktree", ".git/index"} {
		add(filepath.Join(m.r.Dir, filepath.FromSlash(rel)))
	}
	// Every file of the profiles (committed and personal) and of outfit's own directory.
	for _, root := range []string{filepath.Join(m.r.Dir, ".outfit"), filepath.Join(m.r.Dir, ".git", "outfit")} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && !strings.HasSuffix(path, "sync.lock") {
				add(path)
			}
			return nil
		})
	}
	return out
}

func (m *model) assertSyncIdempotent() {
	m.t.Helper()
	m.outfit("sync")
	before := m.mtimes()
	o := m.outfit("sync", "--json")
	var j struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal([]byte(o.Stdout), &j); err != nil {
		m.rt.Fatalf("sync --json is not JSON (%v): %+v", err, o)
	}
	if j.Changed {
		m.rt.Fatalf("a second sync still changed something: %s\nsteps:\n%s", o.Stdout, strings.Join(m.steps, "\n"))
	}
	after := m.mtimes()
	for k, v := range before {
		if after[k] != v {
			m.rt.Fatalf("a second sync touched %s (%s -> %s)\nsteps:\n%s", k, v, after[k], strings.Join(m.steps, "\n"))
		}
	}
}

func TestProperties(t *testing.T) {
	testkit.SkipIfShort(t)
	bin := testkit.BuildOutfit(t, "")
	checks := "8"
	if v := os.Getenv("OUTFIT_PROPERTY_CHECKS"); v != "" {
		checks = v
	}
	if f := flag.Lookup("rapid.checks"); f != nil && f.Value.String() == f.DefValue {
		_ = flag.Set("rapid.checks", checks)
	}
	all := steps()
	rapid.Check(t, func(rt *rapid.T) {
		m := &model{t: t, rt: rt, bin: bin}
		m.r = testkit.New(t, testkit.Team, testkit.WithUpstream(), testkit.WithConflictBranch())
		if o := m.r.RunOutfit(bin, nil, "init"); o.Code != 0 {
			rt.Fatalf("init: %+v", o)
		}
		n := rapid.IntRange(3, 12).Draw(rt, "steps")
		for i := 0; i < n; i++ {
			s := rapid.SampledFrom(all).Draw(rt, "step")
			wasClean := m.statusClean()
			pendingBefore := m.pending
			m.logf("== step %d: %s (clean before: %v, edit pending: %v)", i+1, s.name, wasClean, pendingBefore)
			s.run(m)
			m.check(s.name)
			// (Not while an edit is pending: an editor's save that sync absorbs, or an edit
			// through a link, makes the profile's file differ from git, which is the point. It
			// is pending from the edit until the tree is clean again.)
			if s.name == "edit-through-the-link" || s.name == "editor-atomic-save" {
				m.pending = true
			} else if m.statusClean() && !m.pendingAbsorb() {
				m.pending = false
			}
			if s.clean && wasClean && !pendingBefore && !m.statusClean() {
				out, _ := m.r.GitErr("status", "--short")
				rt.Fatalf("a clean status became dirty after %s:\n%s\nsteps:\n%s", s.name, out, strings.Join(m.steps, "\n"))
			}
		}
		m.assertSyncIdempotent()
		m.check("final sync")
		// Each linked worktree too: a second sync there changes nothing either.
		for _, wt := range m.wts {
			m.r.RunOutfitIn(wt, m.bin, nil, "sync")
			o := m.r.RunOutfitIn(wt, m.bin, nil, "sync", "--json")
			var j struct {
				Changed bool `json:"changed"`
			}
			if err := json.Unmarshal([]byte(o.Stdout), &j); err != nil || j.Changed {
				rt.Fatalf("a second sync in %s: err=%v out=%s", wt, err, o.Stdout)
			}
		}
	})
}
