package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/prosperity-solutions/outfit/internal/testkit"
)

// Scenario 7 (docs/HANDOVER.md §7.4): "Killed at every injected fault point and rerun,
// sync converges with nothing lost." A binary built with -tags faultinject is killed
// (SIGKILL, like power loss) at the Nth fault point of one operation, for every N the
// operation has; then a plain `outfit sync` runs and the tree must be consistent with no
// user byte gone.

type faultScenario struct {
	name string
	// setup brings a fresh fixture to the state the operation starts from (no faults).
	setup func(t *testing.T, r *testkit.Repo, bin string, tokens *[]string)
	// op is the operation that gets killed.
	op []string
	// recover is what runs afterwards (twice); a plain sync, or a second uninstall/init.
	recover []string
	// uninstall scenarios must leave `git config --list` exactly as it was before init.
	uninstalls bool
	// needsHuman scenarios legitimately end in "a person must look" (exit 3).
	needsHuman bool
	// worktrees is how many linked worktrees the fixture has.
	worktrees int
	// beforeInit scenarios kill `init` itself, so setup runs before it.
	noInit bool
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
		{name: "author-on", setup: use("yolo"), op: []string{"author", "on"}, recover: []string{"sync"}},
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

func TestFaultInjection(t *testing.T) {
	testkit.SkipIfShort(t)
	bin := testkit.BuildOutfit(t, "faultinject")
	for _, sc := range faultScenarios() {
		t.Run(sc.name, func(t *testing.T) {
			// How many points does the operation have? Run it once with the log on.
			count := func() int {
				r := testkit.New(t, testkit.Team, testkit.WithWorktrees(sc.worktrees))
				var tokens []string
				if !sc.noInit {
					if o := r.RunOutfit(bin, nil, "init"); o.Code != 0 {
						t.Fatalf("init: %+v", o)
					}
				}
				sc.setup(t, r, bin, &tokens)
				log := filepath.Join(t.TempDir(), "points.log")
				r.RunOutfit(bin, []string{"OUTFIT_FAULT_LOG=" + log}, sc.op...)
				b, _ := os.ReadFile(log)
				return strings.Count(string(b), "\n")
			}()
			if count == 0 {
				t.Fatalf("%v has no fault points: the scenario tests nothing", sc.op)
			}
			t.Logf("%v has %d fault points", sc.op, count)
			for k := 1; k <= count; k++ {
				k := k
				t.Run("kill-at-"+strconv.Itoa(k), func(t *testing.T) {
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
					o := r.RunOutfit(bin, []string{"OUTFIT_FAULT_AT=" + strconv.Itoa(k)}, sc.op...)
					if !o.Killed {
						t.Fatalf("point %d did not kill the process: %+v", k, o)
					}
					assertConverges(t, r, bin, sc, tokens, before)
				})
			}
		})
	}
}

func assertConverges(t *testing.T, r *testkit.Repo, bin string, sc faultScenario, tokens []string, configBefore string) {
	t.Helper()
	// Rerun until it settles: twice is enough for any single kill.
	var last testkit.Outcome
	for i := 0; i < 2; i++ {
		last = r.RunOutfit(bin, nil, sc.recover...)
		if last.Code != 0 && (!sc.needsHuman || last.Code != 3) {
			t.Fatalf("recovery %v run %d did not simply work: %+v", sc.recover, i+1, last)
		}
	}
	// Nothing lost.
	for _, tok := range tokens {
		if !tokenFound(r, tok) {
			t.Fatalf("user bytes %q are gone after a kill and a rerun", tok)
		}
	}
	// No stray temporary link in the worktree (a killed write's `.tmp-*` and a killed profile
	// copy's `.new-*` are dot-files outfit ignores everywhere), no symlink in history.
	_ = filepath.WalkDir(r.Dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if err == nil && strings.HasPrefix(d.Name(), ".outfit-link") {
			t.Errorf("a leftover of a killed run, %s, is still there", path)
		}
		return nil
	})
	if out := r.Git("status", "--porcelain", "--untracked-files=all"); strings.Contains(out, "?? ") && !sc.uninstalls {
		t.Fatalf("status shows something untracked left behind (a stray link, a temporary file):\n%s", out)
	}
	if out := r.Git("ls-tree", "-r", "HEAD"); strings.Contains(out, "120000 ") {
		t.Fatalf("a symlink is in HEAD:\n%s", out)
	}
	if sc.uninstalls {
		if after := r.Git("config", "--list"); after != configBefore {
			t.Fatalf("git config --list differs after a killed uninstall and a rerun:\n--- before\n%s\n--- after\n%s", configBefore, after)
		}
		if s := r.Git("status", "--short"); strings.TrimSpace(s) != "" {
			t.Fatalf("status is not clean after uninstall:\n%s", s)
		}
		if ls := r.Git("ls-files", "-t"); strings.Contains(ls, "S ") {
			t.Fatalf("a path is still skip-worktree after uninstall:\n%s", ls)
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
		t.Fatalf("status --json: %v %+v", err, st)
	}
	if sj.Sync != nil && (!sj.Sync.InSync || (sj.Sync.NeedsHuman && !sc.needsHuman)) {
		t.Fatalf("not in sync after the rerun: %s", st.Stdout)
	}
	for _, f := range sj.Files {
		fi, err := os.Lstat(r.Path(f.Path))
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s should be a link to the %s profile: %v", f.Path, sj.Profile.Name, err)
		}
	}
	again := r.RunOutfit(bin, nil, "sync", "--json")
	var aj struct {
		Changed bool `json:"changed"`
	}
	if err := json.Unmarshal([]byte(again.Stdout), &aj); err != nil {
		t.Fatalf("sync --json is not JSON (%v): %+v", err, again)
	}
	if aj.Changed {
		t.Fatalf("a further sync still changed something: %s", again.Stdout)
	}
}
