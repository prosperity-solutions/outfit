package plan

import (
	"fmt"
	"testing"
)

// FuzzPlan (HANDOVER.md §7.5): Plan is pure, so every state a worktree can be in is a
// fuzz input. Whatever the facts say, it never panics, is deterministic, returns
// refusals ALONE, and never plans a destructive step on a path that is mid-merge, nor a
// removal or a quarantine where nothing of outfit's stands.
func FuzzPlan(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19})
	f.Add([]byte{255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255})
	f.Add([]byte("AGENTS.md a regular file where the link was"))
	f.Fuzz(func(t *testing.T, data []byte) {
		next := func() byte {
			if len(data) == 0 {
				return 0
			}
			b := data[0]
			data = data[1:]
			return b
		}
		flag := func() bool { return next()&1 == 1 }
		names := []string{"AGENTS.md", "CLAUDE.md", "apps/web/AGENTS.md", "docs/guide.md"}
		w := Want{Exclude: flag(), HidesProfiles: flag()}
		f := Facts{
			SparseOn: flag(), ExpectFiles: flag(), ExcludeHas: flag(), SameProfile: flag(),
			Paths: map[string]PathFacts{}, Recorded: map[string]Record{},
			GitBusy: []string{"", "rebase", "merge"}[int(next())%3],
		}
		if flag() {
			f.SparseText = SparseText(SparseOptions{Hidden: []string{"AGENTS.md"}, HideProfiles: true, Show: []string{"p"}})
		}
		if flag() {
			f.StagedProfiles = []string{".outfit/profiles/x/AGENTS.md"}
		}
		var hidden []string
		for _, n := range names {
			sel := next()
			target := "t/" + n
			if sel&1 == 1 { // the profile swaps it
				w.Swaps = append(w.Swaps, Swap{Path: n, Target: target})
				hidden = append(hidden, n)
			}
			fsk := []Kind{Missing, Link, File, Dir, Other}[int(next())%5]
			pf := PathFacts{
				FS: fsk, Tracked: flag(), SkipWorktree: flag(), Unmerged: flag(), Mode: []string{"100644", "100755", "120000"}[int(next())%3],
				MatchesIndex: flag(), FileHash: []string{"", "a", "b"}[int(next())%3], VariantHash: []string{"", "a", "b"}[int(next())%3],
				VariantBlob: []string{"", "v1", "v2"}[int(next())%3], BaseBlob: []string{"", "b1", "b2"}[int(next())%3],
				SameCommit: flag(), VariantExec: flag(),
			}
			if fsk == Link {
				pf.LinkTarget = []string{target, "elsewhere"}[int(next())%2]
			}
			switch next() % 4 { // both spellings of a stray link
			case 1:
				pf.StrayLinks = []string{n + "~cruft"}
			case 2:
				pf.StrayLinks = []string{".outfit-link~" + n}
			case 3:
				pf.StrayLinks = []string{n + "~cruft", ".outfit-link~" + n}
			}
			f.Paths[n] = pf
			if flag() {
				f.Recorded[n] = Record{Target: target, VariantHash: []string{"", "a", "b"}[int(next())%3], BaseBlob: "b1", SeenBase: []string{"", "b1", "b2"}[int(next())%3], VariantBlob: "v1"}
			}
		}
		if len(w.Swaps) > 0 || flag() {
			w.Sparse = SparseText(SparseOptions{Hidden: hidden, HideProfiles: w.HidesProfiles, Show: []string{"p"}})
		}

		a := Plan(w, f)
		b := Plan(w, f)
		if fmt.Sprint(a) != fmt.Sprint(b) {
			t.Fatalf("Plan is not deterministic: %v vs %v", kinds(a), kinds(b))
		}
		if refused := Refused(a); len(refused) > 0 && len(refused) != len(a) {
			t.Fatalf("a refusal must be returned alone: %v", kinds(a))
		}
		for _, act := range a {
			pf, known := f.Paths[act.Path]
			switch act.Kind {
			case RemoveLink:
				if known && pf.FS != Link {
					t.Fatalf("RemoveLink on %s which is not a link (%v)", act.Path, pf.FS)
				}
				if known && pf.Unmerged {
					t.Fatalf("RemoveLink on an unmerged path %s", act.Path)
				}
			case QuarantineFile, QuarantineBoth, Absorb:
				if act.Kind == Absorb && f.GitBusy != "" && f.Recorded[act.Path].VariantHash != "" && f.Recorded[act.Path].VariantHash == pf.VariantHash && pf.FileHash != pf.VariantHash {
					t.Fatalf("Absorb of an editor's save while git is in the middle of a %s", f.GitBusy)
				}
				if !known || pf.FS != File {
					t.Fatalf("%s on %s which is not a regular file", act.Kind, act.Path)
				}
				if pf.Unmerged {
					t.Fatalf("%s on an unmerged path %s", act.Kind, act.Path)
				}
			case LinkPath:
				if pf.FS == Dir || pf.FS == Other {
					t.Fatalf("LinkPath over %s, which is a directory or something else", act.Path)
				}
				// A regular file is only ever replaced by a link when it is the committed
				// copy, untouched (anything else is user bytes: absorb or quarantine).
				if pf.FS == File && (!pf.Tracked || !pf.MatchesIndex || pf.Unmerged) {
					t.Fatalf("LinkPath over %s, a regular file that is not the untouched committed copy (%+v)", act.Path, pf)
				}
			}
		}
	})
}

func TestPlanFuzzFixtureIsMeaningful(t *testing.T) {
	// A guard for the fuzz body's idea of a state: a plain missing link plans something.
	if kinds(Plan(Want{Swaps: []Swap{{Path: "A", Target: "t"}}}, Facts{Paths: map[string]PathFacts{"A": {FS: Missing, Tracked: true, VariantHash: "a", Mode: "100644"}}})) == nil {
		t.Fatal("the fixture plans nothing")
	}
}
