package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/testkit"
)

// Scenario 7 (docs/HANDOVER.md §7.4): "Killed at every injected fault point and rerun,
// sync converges with nothing lost." A binary built with -tags faultinject is killed
// (SIGKILL, like power loss) at the Nth fault point of one operation, for every N the
// operation has; then a rerun (a plain `outfit sync`, or `outfit uninstall` for the
// scenarios that end by undoing everything) must converge, with no user byte gone.

type faultScenario struct {
	name string
	// setup brings a fresh fixture to the state the operation starts from (no faults).
	setup func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string)
	// op is the operation that gets killed.
	op []string
	// recover is what runs afterwards (twice): a plain sync, or `uninstall`.
	recover []string
	// uninstall scenarios must leave `git config --list` exactly as it was before init.
	uninstalls bool
	// needsHuman scenarios legitimately end in "a person must look" (exit 3).
	needsHuman bool
	// worktrees is how many linked worktrees the fixture has.
	worktrees int
	// noInit scenarios kill `init` itself, so setup runs before it.
	noInit bool
	// viaLink runs the killed operation from a symlinked spelling of the repository's path,
	// and the recovery from the physical one (an IDE and a terminal rarely agree).
	viaLink bool
}

// opDir is where the killed operation runs.
func (sc faultScenario) opDir(t *testing.T, r *testkit.Repo) string {
	if !sc.viaLink {
		return r.Dir
	}
	link := filepath.Join(t.TempDir(), "via")
	if err := os.Symlink(r.Dir, link); err != nil {
		t.Fatal(err)
	}
	return link
}

func writeToken(t *testing.T, r *testkit.Repo, tokens *[]string, path string, atomic bool) {
	t.Helper()
	tok := fmt.Sprintf("token-%d", len(*tokens)+1)
	*tokens = append(*tokens, tok)
	p := r.Path(path)
	old, _ := os.ReadFile(p)
	content := string(old) + tok + "\n"
	if atomic {
		tmp := p + ".editor"
		if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, p); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(tok + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
}

// tokenFound looks for tok in the worktree and in outfit's own directories (profiles,
// quarantine) of the clone.
func tokenFound(r *testkit.Repo, tok string) bool {
	found := false
	for _, root := range []string{r.Dir, filepath.Join(r.Dir, ".git", "outfit")} {
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || found {
				return nil
			}
			if d.IsDir() && d.Name() == ".git" && root == r.Dir {
				return filepath.SkipDir
			}
			if d.Type().IsRegular() {
				if b, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(b), tok) {
					found = true
				}
			}
			return nil
		})
	}
	return found
}

func faultScenarios() []faultScenario {
	use := func(name string) func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
		return func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			if o := r.RunOutfit(bin, nil, "use", name); o.Code != 0 {
				t.Fatalf("setup use %s: %+v", name, o)
			}
		}
	}
	return []faultScenario{
		{name: "use-a-profile", setup: func(*testing.T, *testkit.Repo, string, *[]string) {}, op: []string{"use", "yolo"}, recover: []string{"sync"}},
		{name: "switch-profiles", setup: use("yolo"), op: []string{"use", "lean"}, recover: []string{"sync"}},
		{name: "back-to-base", setup: use("yolo"), op: []string{"use", "base"}, recover: []string{"sync"}},
		{name: "absorb-an-editors-save", setup: func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			use("yolo")(t, r, bin, tokens)
			writeToken(t, r, tokens, "AGENTS.md", true)
		}, op: []string{"sync"}, recover: []string{"sync"}},
		{name: "quarantine-both", needsHuman: true, setup: func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			use("yolo")(t, r, bin, tokens)
			writeToken(t, r, tokens, "AGENTS.md", false) // through the link
			if o := r.RunOutfit(bin, nil, "sync"); o.Code != 0 {
				t.Fatalf("setup sync: %+v", o)
			}
			writeToken(t, r, tokens, "AGENTS.md", true) // an editor replaces the link
		}, op: []string{"sync"}, recover: []string{"sync"}},
		{name: "replace-a-directory", setup: replaceSetup, op: []string{"use", "repl"}, recover: []string{"sync"}},
		{name: "uninstall-with-a-replaced-directory", uninstalls: true, setup: func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			replaceSetup(t, r, bin, tokens)
			if o := r.RunOutfit(bin, nil, "use", "repl"); o.Code != 0 {
				t.Fatalf("setup use repl: %+v", o)
			}
		}, op: []string{"uninstall"}, recover: []string{"uninstall"}},
		{name: "author-on", setup: use("yolo"), op: []string{"author", "on"}, recover: []string{"sync"}},
		{name: "author-on-one", setup: use("yolo"), op: []string{"author", "on", "lean"}, recover: []string{"sync"}},
		{name: "author-off", setup: func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			use("yolo")(t, r, bin, tokens)
			if o := r.RunOutfit(bin, nil, "author", "on"); o.Code != 0 {
				t.Fatalf("setup author on: %+v", o)
			}
		}, op: []string{"author", "off"}, recover: []string{"sync"}},
		{name: "use-a-local-profile", setup: func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			if o := r.RunOutfit(bin, nil, "new", "mine", "--from", "yolo"); o.Code != 0 {
				t.Fatalf("setup new: %+v", o)
			}
		}, op: []string{"use", "mine"}, recover: []string{"sync"}},
		{name: "uninstall", uninstalls: true, setup: use("yolo"), op: []string{"uninstall"}, recover: []string{"uninstall"}},
		{name: "local-profile-killed-via-a-symlinked-path-then-uninstall", uninstalls: true, viaLink: true, setup: func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			if o := r.RunOutfit(bin, nil, "new", "mine", "--from", "yolo"); o.Code != 0 {
				t.Fatalf("setup new: %+v", o)
			}
		}, op: []string{"use", "mine"}, recover: []string{"uninstall"}},
		{name: "uninstall-in-a-worktree-farm", uninstalls: true, worktrees: 1, setup: use("yolo"), op: []string{"uninstall"}, recover: []string{"uninstall"}},
		{name: "use-then-uninstall", uninstalls: true, setup: func(*testing.T, *testkit.Repo, string, *[]string) {}, op: []string{"use", "yolo"}, recover: []string{"uninstall"}},
		{name: "init", uninstalls: true, noInit: true, setup: func(*testing.T, *testkit.Repo, string, *[]string) {}, op: []string{"init"}, recover: []string{"uninstall"}},
		{name: "promote", setup: func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
			if o := r.RunOutfit(bin, nil, "new", "mine", "--from", "yolo"); o.Code != 0 {
				t.Fatalf("setup new: %+v", o)
			}
		}, op: []string{"promote", "mine"}, recover: []string{"sync"}},
	}
}

// faultCtx says which kill a failing check belongs to, and how to rerun just that.
type faultCtx struct {
	sc     faultScenario
	k      int
	points []string // every point of the operation, in order (index 0 is point 1)
}

func (fc faultCtx) subtest() string {
	return fmt.Sprintf("kill-at-%02d-%s", fc.k, slug(fc.points[fc.k-1]))
}

func (fc faultCtx) fatalf(t *testing.T, format string, a ...any) {
	t.Helper()
	t.Fatalf("%s\n\n-- killed at point %d of `outfit %s`: %q\n-- the points of this operation, in order:\n%s\n-- reproduce: go test ./internal/e2e -count=1 -run '^TestFaultInjection$/^%s$/^%s$'",
		fmt.Sprintf(format, a...), fc.k, strings.Join(fc.sc.op, " "), fc.points[fc.k-1], numbered(fc.points), fc.sc.name, fc.subtest())
}

func numbered(points []string) string {
	var b strings.Builder
	for i, p := range points {
		fmt.Fprintf(&b, "   %2d %s\n", i+1, p)
	}
	return b.String()
}

var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

// slug makes a point name usable in a subtest name (and in -run).
func slug(s string) string {
	s = strings.Trim(nonWord.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func TestFaultInjection(t *testing.T) {
	testkit.SkipIfShort(t)
	bin := testkit.BuildOutfit(t, "faultinject")
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			// Which points does the operation have? Run it once with the log on.
			points := func() []string {
				r := testkit.New(t, testkit.Team, testkit.WithWorktrees(sc.worktrees))
				var tokens []string
				if !sc.noInit {
					if o := r.RunOutfit(bin, nil, "init"); o.Code != 0 {
						t.Fatalf("init: %+v", o)
					}
				}
				sc.setup(t, r, bin, &tokens)
				log := filepath.Join(t.TempDir(), "points.log")
				r.RunOutfitIn(sc.opDir(t, r), bin, []string{"OUTFIT_FAULT_LOG=" + log}, sc.op...)
				b, _ := os.ReadFile(log)
				var names []string
				for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
					if _, name, ok := strings.Cut(line, " "); ok {
						names = append(names, name)
					}
				}
				return names
			}()
			if len(points) == 0 {
				t.Fatalf("%v has no fault points: the scenario tests nothing", sc.op)
			}
			t.Logf("%v has %d fault points:\n%s", sc.op, len(points), numbered(points))
			for k := 1; k <= len(points); k++ {
				fc := faultCtx{sc: sc, k: k, points: points}
				t.Run(fc.subtest(), func(t *testing.T) {
					t.Parallel() // each kill point has its own fixture and its own process
					r := testkit.New(t, testkit.Team, testkit.WithWorktrees(sc.worktrees))
					var tokens []string
					before := r.Git("config", "--list") // before init: uninstall must bring it back
					if !sc.noInit {
						if o := r.RunOutfit(bin, nil, "init"); o.Code != 0 {
							t.Fatalf("init: %+v", o)
						}
					}
					sc.setup(t, r, bin, &tokens)
					o := r.RunOutfitIn(sc.opDir(t, r), bin, []string{"OUTFIT_FAULT_AT=" + strconv.Itoa(k)}, sc.op...)
					if !o.Killed {
						fc.fatalf(t, "the point did not kill the process (the operation changed): %+v", o)
					}
					assertConverges(t, r, bin, fc, tokens, before)
				})
			}
		})
	}
}

func assertConverges(t *testing.T, r *testkit.Repo, bin string, fc faultCtx, tokens []string, configBefore string) {
	t.Helper()
	sc := fc.sc
	// Rerun until it settles: twice is enough for any single kill.
	var last testkit.Outcome
	for i := 0; i < 2; i++ {
		last = r.RunOutfit(bin, nil, sc.recover...)
		if last.Code != 0 && (!sc.needsHuman || last.Code != 3) {
			fc.fatalf(t, "recovery %v run %d did not simply work: stdout=%q stderr=%q exit=%d", sc.recover, i+1, clip(last.Stdout, 400), clip(last.Stderr, 800), last.Code)
		}
	}
	// Nothing lost.
	for _, tok := range tokens {
		if !tokenFound(r, tok) {
			fc.fatalf(t, "user bytes %q are gone after a kill and a rerun", tok)
		}
	}
	// No stray temporary link in the worktree (a killed write's `.tmp-*` and a killed profile
	// copy's `.new-*` are dot-files outfit ignores everywhere), no symlink in history.
	_ = filepath.WalkDir(r.Dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if err == nil && strings.HasPrefix(d.Name(), ".outfit-link") {
			fc.fatalf(t, "a leftover of a killed run, %s, is still there", path)
		}
		return nil
	})
	if out := r.Git("status", "--porcelain", "--untracked-files=all"); strings.Contains(out, "?? ") && !sc.uninstalls {
		fc.fatalf(t, "status shows something untracked left behind (a stray link, a temporary file):\n%s", out)
	}
	if out := r.Git("ls-tree", "-r", "HEAD"); strings.Contains(out, "120000 ") {
		fc.fatalf(t, "a symlink is in HEAD:\n%s", out)
	}
	if sc.uninstalls {
		if after := r.Git("config", "--list"); after != configBefore {
			fc.fatalf(t, "git config --list differs after a killed uninstall and a rerun:\n--- before\n%s\n--- after\n%s", configBefore, after)
		}
		if s := r.Git("status", "--short"); strings.TrimSpace(s) != "" {
			fc.fatalf(t, "status is not clean after uninstall:\n%s", s)
		}
		if ls := r.Git("ls-files", "-t"); strings.Contains(ls, "S ") {
			fc.fatalf(t, "a path is still skip-worktree after uninstall:\n%s", ls)
		}
		return
	}
	// Consistent and quiet: the profile in config is the profile on disk, and a further
	// sync changes nothing.
	st := r.RunOutfit(bin, nil, "status", "--json")
	var sj struct {
		Profile struct{ Name string } `json:"profile"`
		Sync    *struct {
			InSync     bool `json:"in_sync"`
			NeedsHuman bool `json:"needs_human"`
		} `json:"sync"`
		Files []struct{ Path string } `json:"files"`
	}
	if err := json.Unmarshal([]byte(st.Stdout), &sj); err != nil {
		fc.fatalf(t, "status --json: %v %+v", err, st)
	}
	if sj.Sync != nil && (!sj.Sync.InSync || (sj.Sync.NeedsHuman && !sc.needsHuman)) {
		fc.fatalf(t, "not in sync after the rerun (status --json): %s", clip(st.Stdout, 700))
	}
	for _, f := range sj.Files {
		fi, err := os.Lstat(r.Path(f.Path))
		if err != nil || (fi.Mode()&os.ModeSymlink == 0 && !hardLinked(fi)) {
			fc.fatalf(t, "%s should be a link (a symlink, or a hard link for a local profile) to the %s profile: %v", f.Path, sj.Profile.Name, err)
		}
	}
	again := r.RunOutfit(bin, nil, "sync", "--json")
	var aj struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal([]byte(again.Stdout), &aj); err != nil {
		fc.fatalf(t, "sync --json is not JSON (%v): %+v", err, again)
	}
	if aj.Changed {
		fc.fatalf(t, "a further sync still changed something: %s", again.Stdout)
	}
}

// replaceSetup: the team has skills/a.md and skills/b.md; the profile `repl` replaces the directory, brings its own a.md and files the team lacks.
var replaceSetup = func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string) {
	// The team has skills/a.md and skills/b.md; the profile `repl` replaces the directory and brings its own a.md.
	r.Write("skills/a.md", "team a\n")
	r.Write("skills/b.md", "team b\n")
	r.Write(".outfit/profiles/repl/profile.config", "[outfit \"profile\"]\n    replace = skills\n")
	r.Write(".outfit/profiles/repl/skills/a.md", "my a\n")
	r.Write(".outfit/profiles/repl/skills/new.md", "my new\n")         // only the profile has it
	r.Write(".outfit/profiles/repl/skills/deep/x.md", "my deep one\n") // nor the directory
	r.Git("add", "skills")
	r.Git("add", "--sparse", "-f", ".outfit/profiles/repl")
	r.Git("commit", "-qm", "feat: replace skills")
}

// hardLinked says a regular file has more than one name: a local or worktree profile's file, hard-linked
// into the worktree (internal/apply/hardlink.go).
func hardLinked(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && fi.Mode().IsRegular() && st.Nlink > 1
}
