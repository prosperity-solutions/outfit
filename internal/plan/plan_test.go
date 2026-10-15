package plan

import (
	"strings"
	"testing"
)

func kinds(as []Action) []ActionKind {
	var out []ActionKind
	for _, a := range as {
		out = append(out, a.Kind)
	}
	return out
}

func has(as []Action, k ActionKind, path string) bool {
	for _, a := range as {
		if a.Kind == k && a.Path == path {
			return true
		}
	}
	return false
}

const tgt = "../../.outfit/profiles/yolo/AGENTS.md"

// inSync builds Facts for one swapped path AGENTS.md that is correctly linked.
func inSync() (Want, Facts) {
	w := Want{Swaps: []Swap{{Path: "AGENTS.md", Target: tgt}}, Sparse: SparseText(SparseOptions{Hidden: []string{"AGENTS.md"}, HideProfiles: true, Active: "yolo"}), Exclude: true}
	f := Facts{
		SparseOn: true, SparseText: w.Sparse, ExpectFiles: true, ExcludeHas: true,
		Paths:    map[string]PathFacts{"AGENTS.md": {FS: Link, LinkTarget: tgt, Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "h1"}},
		Recorded: map[string]Record{"AGENTS.md": {Target: tgt, VariantHash: "h1"}},
	}
	return w, f
}

func TestPlanInSyncIsEmpty(t *testing.T) {
	w, f := inSync()
	if got := Plan(w, f); len(got) != 0 {
		t.Fatalf("an in-sync worktree must plan nothing, got %v", kinds(got))
	}
}

func TestPlanBaseWithNothingToHideIsEmptyAndSparseOff(t *testing.T) {
	if got := Plan(Want{}, Facts{}); len(got) != 0 {
		t.Fatalf("%v", kinds(got))
	}
}

func TestPlanFirstSwap(t *testing.T) {
	w, _ := inSync()
	f := Facts{Paths: map[string]PathFacts{"AGENTS.md": {FS: File, Tracked: true, Mode: "100644", MatchesIndex: true, VariantHash: "h1"}}}
	got := Plan(w, f)
	want := []ActionKind{SetExpectFiles, WriteSparse, LinkPath, AddExclude}
	if strings.Join(toStrings(kinds(got)), ",") != strings.Join(toStrings(want), ",") {
		t.Fatalf("kinds = %v, want %v", kinds(got), want)
	}
	if !got[2].Replaces || got[2].Target != tgt {
		t.Fatalf("link action = %+v", got[2])
	}
}

func toStrings(ks []ActionKind) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = string(k)
	}
	return out
}

func TestPlanRefusals(t *testing.T) {
	cases := map[string]func(w *Want, f *Facts){
		"foreign sparse checkout": func(w *Want, f *Facts) {
			f.SparseOn, f.SparseText = true, "/*\n!/something\n"
		},
		"base file edited, never swapped": func(w *Want, f *Facts) {
			f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, Mode: "100644", MatchesIndex: false, VariantHash: "h1"}
		},
		"a directory in the way": func(w *Want, f *Facts) {
			f.Paths["AGENTS.md"] = PathFacts{FS: Dir, Tracked: true, Mode: "100644", VariantHash: "h1"}
		},
		"the base file is itself a symlink": func(w *Want, f *Facts) {
			f.Paths["AGENTS.md"] = PathFacts{FS: Link, LinkTarget: "x", Tracked: true, Mode: "120000", VariantHash: "h1"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w, f := inSync()
			f.SparseOn, f.SparseText, f.Recorded = false, "", map[string]Record{}
			f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, Mode: "100644", MatchesIndex: true, VariantHash: "h1"}
			mutate(&w, &f)
			got := Plan(w, f)
			if len(got) == 0 || len(Refused(got)) != len(got) {
				t.Fatalf("expected only refusals, got %v", kinds(got))
			}
		})
	}
	// An uncommitted edit's refusal names the fix.
	w, f := inSync()
	f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, Mode: "100644", VariantHash: "h1"}
	f.Recorded = map[string]Record{}
	f.SparseOn, f.SparseText = false, ""
	got := Plan(w, f)
	if len(got) != 1 || !strings.Contains(got[0].Fix, "git stash push -- AGENTS.md") {
		t.Fatalf("%+v", got)
	}
}

func TestPlanForeignSparseIsLeftAloneWhenOutfitHasNothingToDo(t *testing.T) {
	f := Facts{SparseOn: true, SparseText: "/*\n!/x\n"}
	if got := Plan(Want{}, f); len(got) != 0 {
		t.Fatalf("outfit touched a sparse checkout it has no use for: %v", kinds(got))
	}
}

func TestPlanMissingAndDanglingLinksAreRecreated(t *testing.T) {
	w, f := inSync()
	f.Paths["AGENTS.md"] = PathFacts{FS: Missing, Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "h1"}
	if got := Plan(w, f); !has(got, LinkPath, "AGENTS.md") {
		t.Fatalf("%v", kinds(got))
	}
	f.Paths["AGENTS.md"] = PathFacts{FS: Link, LinkTarget: "old/target", Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "h1"}
	got := Plan(w, f)
	if !has(got, LinkPath, "AGENTS.md") || got[0].Target != tgt {
		t.Fatalf("%v", got)
	}
}

func TestPlanAbsorbAndQuarantine(t *testing.T) {
	w, f := inSync()
	// An atomic save replaced the link; the variant is unchanged since linking.
	f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "h1"}
	got := Plan(w, f)
	if !has(got, Absorb, "AGENTS.md") || NeedsHuman(got) {
		t.Fatalf("expected a plain absorb, got %v", kinds(got))
	}
	// The variant ALSO changed since linking: both are saved, nothing is touched.
	f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "h2"}
	got = Plan(w, f)
	if !has(got, QuarantineBoth, "AGENTS.md") || !NeedsHuman(got) || has(got, LinkPath, "AGENTS.md") || has(got, Absorb, "AGENTS.md") {
		t.Fatalf("expected a quarantine and no relink, got %v", kinds(got))
	}
	// No record at all (state lost): cannot prove the variant is unchanged.
	f.Recorded = map[string]Record{}
	f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "h1"}
	if got = Plan(w, f); !has(got, QuarantineBoth, "AGENTS.md") {
		t.Fatalf("without a record, absorbing is a guess: %v", kinds(got))
	}
}

func TestPlanRestoresALostSkipWorktreeBit(t *testing.T) {
	w, f := inSync()
	pf := f.Paths["AGENTS.md"]
	pf.SkipWorktree = false // `git reset -- AGENTS.md` does this
	f.Paths["AGENTS.md"] = pf
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != RepairSkip || len(got[0].Paths) != 1 || got[0].Paths[0] != "AGENTS.md" {
		t.Fatalf("%+v", got)
	}
	// With a sparse write already planned the bits come from git itself.
	f.SparseText = ""
	if got := Plan(w, f); hasKind(got, RepairSkip) {
		t.Fatalf("a sparse write sets the bits: %v", kinds(got))
	}
}

func TestPlanQuarantineClearsWhenFileAndVariantBecomeIdentical(t *testing.T) {
	w, f := inSync()
	f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, SkipWorktree: true, Mode: "100644", FileHash: "same", VariantHash: "same"}
	f.Recorded["AGENTS.md"] = Record{Target: tgt, VariantHash: "older"}
	got := Plan(w, f)
	if !has(got, Absorb, "AGENTS.md") || NeedsHuman(got) {
		t.Fatalf("an identical file and variant must relink, got %v", kinds(got))
	}
}

func TestPlanDirtyBaseFileIsRefusedEvenWhileTheVariantIsStillHidden(t *testing.T) {
	// Right after a switch the new profile's file is not on disk (VariantHash ""):
	// the refusal must still come first, so nothing is applied.
	w := Want{Swaps: []Swap{{Path: "a.md", Target: "t"}}}
	f := Facts{Paths: map[string]PathFacts{"a.md": {FS: File, Tracked: true, Mode: "100644", MatchesIndex: false, VariantHash: ""}}}
	if got := Plan(w, f); len(got) != 1 || got[0].Kind != Refuse {
		t.Fatalf("%v", kinds(got))
	}
}

func TestPlanRefusesToHideProfilesWhileProfileFilesAreOnlyStaged(t *testing.T) {
	w := Want{Sparse: SparseText(SparseOptions{HideProfiles: true}), HidesProfiles: true, Exclude: true}
	f := Facts{ExpectFiles: true, ExcludeHas: true, StagedProfiles: []string{".outfit/profiles/new/AGENTS.md"}}
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != Refuse || !strings.Contains(got[0].Fix, "git commit") {
		t.Fatalf("%v %+v", kinds(got), got)
	}
	// Patterns that already hide them change nothing, so nothing to refuse.
	f.SparseText, f.SparseOn = w.Sparse, true
	if got := Plan(w, f); len(got) != 0 {
		t.Fatalf("%v", kinds(got))
	}
}

func TestPlanRecordsAVariantEditedThroughTheLink(t *testing.T) {
	w, f := inSync()
	f.Paths["AGENTS.md"] = PathFacts{FS: Link, LinkTarget: tgt, Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "h-edited"}
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != RecordHash {
		t.Fatalf("%v", kinds(got))
	}
}

func TestPlanSwitchRemovesObsoleteLinksBeforeTheSparseChange(t *testing.T) {
	// yolo swapped AGENTS.md and CLAUDE.md; lean swaps only apps/web/AGENTS.md.
	w := Want{Swaps: []Swap{{Path: "apps/web/AGENTS.md", Target: "../../.outfit/profiles/lean/apps/web/AGENTS.md"}},
		Sparse: SparseText(SparseOptions{Hidden: []string{"apps/web/AGENTS.md"}, HideProfiles: true, Active: "lean"}), Exclude: true}
	f := Facts{
		SparseOn: true, ExpectFiles: true, ExcludeHas: true,
		SparseText: SparseText(SparseOptions{Hidden: []string{"AGENTS.md", "CLAUDE.md"}, HideProfiles: true, Active: "yolo"}),
		Paths: map[string]PathFacts{
			"AGENTS.md":          {FS: Link, LinkTarget: "x", Tracked: true, SkipWorktree: true, Mode: "100644"},
			"CLAUDE.md":          {FS: Link, LinkTarget: "y", Tracked: true, SkipWorktree: true, Mode: "100644"},
			"apps/web/AGENTS.md": {FS: Missing, Tracked: true, SkipWorktree: false, Mode: "100644", VariantHash: "v"},
		},
		Recorded: map[string]Record{"AGENTS.md": {}, "CLAUDE.md": {}},
	}
	got := Plan(w, f)
	var order []string
	for _, a := range got {
		order = append(order, string(a.Kind)+" "+a.Path)
	}
	want := "remove-link AGENTS.md,remove-link CLAUDE.md,write-sparse ,link apps/web/AGENTS.md"
	if strings.Join(order, ",") != want {
		t.Fatalf("order = %s\nwant    %s", strings.Join(order, ","), want)
	}
}

func TestPlanObsoleteRegularFileIsQuarantinedNotLost(t *testing.T) {
	w := Want{}
	f := Facts{Paths: map[string]PathFacts{"AGENTS.md": {FS: File, Tracked: true, SkipWorktree: true}}, Recorded: map[string]Record{"AGENTS.md": {}}}
	got := Plan(w, f)
	if !has(got, QuarantineFile, "AGENTS.md") || !NeedsHuman(got) {
		t.Fatalf("%v", kinds(got))
	}
}

func TestPlanDisablesManagedSparseWhenNothingNeedsIt(t *testing.T) {
	f := Facts{SparseOn: true, ExpectFiles: true, SparseText: SparseText(SparseOptions{HideProfiles: true})}
	got := Plan(Want{}, f)
	if len(got) != 1 || got[0].Kind != DisableSparse {
		t.Fatalf("%v", kinds(got))
	}
}

func TestPlanSkipsUntrackedMissingVariantAndConflicts(t *testing.T) {
	w := Want{Swaps: []Swap{{Path: "new.md", Target: "t1"}, {Path: "gone.md", Target: "t2"}, {Path: "merge.md", Target: "t3"}}}
	f := Facts{Paths: map[string]PathFacts{
		"new.md":   {FS: Missing, Tracked: false, VariantHash: "v"},
		"gone.md":  {FS: Missing, Tracked: true, Mode: "100644", VariantHash: ""},
		"merge.md": {FS: File, Tracked: false, Unmerged: true, VariantHash: "v"},
	}}
	got := Plan(w, f)
	if !has(got, Skip, "new.md") || !has(got, Skip, "gone.md") || !has(got, Conflict, "merge.md") || !NeedsHuman(got) {
		t.Fatalf("%v", got)
	}
	for _, a := range got {
		if a.Kind == LinkPath || a.Kind == WriteSparse {
			t.Fatalf("a skipped path was acted on: %+v", a)
		}
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	w := Want{Swaps: []Swap{{Path: "b", Target: "t"}, {Path: "a", Target: "t"}, {Path: "c", Target: "t"}}}
	f := Facts{Paths: map[string]PathFacts{}}
	for _, p := range []string{"a", "b", "c"} {
		f.Paths[p] = PathFacts{FS: Missing, Tracked: true, SkipWorktree: true, Mode: "100644", VariantHash: "v"}
	}
	first := Plan(w, f)
	for i := 0; i < 20; i++ {
		got := Plan(w, f)
		if len(got) != len(first) {
			t.Fatal("different length")
		}
		for j := range got {
			if got[j].Describe() != first[j].Describe() {
				t.Fatalf("run %d differs at %d: %s vs %s", i, j, got[j].Describe(), first[j].Describe())
			}
		}
	}
}

func TestPlanStaleBaseKeepsTheSwapAndSaysSo(t *testing.T) {
	w, f := inSync()
	f.Recorded["AGENTS.md"] = Record{Target: tgt, VariantHash: "h1", BaseBlob: "old", SeenBase: "old"}
	pf := f.Paths["AGENTS.md"]
	pf.BaseBlob = "new"
	f.Paths["AGENTS.md"] = pf
	got := Plan(w, f)
	var stale *Action
	var records int
	for i, a := range got {
		switch a.Kind {
		case Stale:
			stale = &got[i]
		case RecordHash:
			records++ // the base blob the sync saw is remembered
		default:
			t.Fatalf("unexpected %v", kinds(got))
		}
	}
	if stale == nil || records != 1 || stale.Fix != "outfit diff AGENTS.md" || !strings.Contains(stale.Reason, "outfit ack AGENTS.md") {
		t.Fatalf("%v %+v", kinds(got), got)
	}
	// Same blob: nothing to say. An older state file with nothing recorded: say nothing.
	pf.BaseBlob = "old"
	f.Paths["AGENTS.md"] = pf
	if got := Plan(w, f); len(got) != 0 {
		t.Fatalf("%v", kinds(got))
	}
	f.Recorded["AGENTS.md"] = Record{Target: tgt, VariantHash: "h1"}
	pf.BaseBlob = "new"
	f.Paths["AGENTS.md"] = pf
	for _, a := range Plan(w, f) {
		if a.Kind == Stale {
			t.Fatalf("no recorded base: nothing to compare with")
		}
	}
}

func TestPlanVariantRemovedFromTheActiveProfileIsUnswappedWithAReason(t *testing.T) {
	w, f := inSync()
	w.Swaps = nil
	w.Sparse = SparseText(SparseOptions{HideProfiles: true, Active: "yolo"})
	f.SameProfile = true
	got := Plan(w, f)
	var found bool
	for _, a := range got {
		if a.Kind == RemoveLink && strings.Contains(a.Reason, "AGENTS.md is no longer in the profile") {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v %+v", kinds(got), got)
	}
	// A switch to another profile removes the same link without the note.
	f.SameProfile = false
	for _, a := range Plan(w, f) {
		if a.Kind == RemoveLink && a.Reason != "" {
			t.Fatalf("a switch should not warn: %+v", a)
		}
	}
}

func TestPlanLeavesAConflictedPathAlone(t *testing.T) {
	w, f := inSync()
	pf := f.Paths["AGENTS.md"]
	pf.FS, pf.Tracked, pf.Unmerged, pf.SkipWorktree = File, false, true, false
	f.Paths["AGENTS.md"] = pf
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != Conflict {
		t.Fatalf("%v", kinds(got))
	}
}

func TestAdaptedNeedsOneCommitThatMovedBaseAndVariantTogether(t *testing.T) {
	rec := Record{BaseBlob: "b1", SeenBase: "b1", VariantBlob: "v1"}
	pf := PathFacts{BaseBlob: "b2", VariantBlob: "v2", SameCommit: true}
	if !Adapted(rec, pf) {
		t.Fatal("one commit moved both: adapted")
	}
	pf.SameCommit = false
	if Adapted(rec, pf) {
		t.Fatal("two commits (an unrelated profile fix next to a base change) must stay stale")
	}
	pf.SameCommit = true
	rec.SeenBase = "b2" // the base change was already reported; a profile commit alone does not clear it
	if Adapted(rec, pf) {
		t.Fatal("a profile commit after the base was reported must not clear it")
	}
	rec.SeenBase, pf.VariantBlob = "b1", "v1"
	if Adapted(rec, pf) {
		t.Fatal("the variant did not move")
	}
}

func TestStaleBaseKnownBasesSurviveABranchSwitch(t *testing.T) {
	w, f := inSync()
	f.Recorded["AGENTS.md"] = Record{Target: tgt, VariantHash: "h1", BaseBlob: "feature", SeenBase: "feature", KnownBases: []string{"main"}}
	pf := f.Paths["AGENTS.md"]
	pf.BaseBlob = "main"
	f.Paths["AGENTS.md"] = pf
	for _, a := range Plan(w, f) {
		if a.Kind == Stale {
			t.Fatal("main was acknowledged before: not stale again")
		}
	}
	pf.BaseBlob = "third"
	f.Paths["AGENTS.md"] = pf
	var stale bool
	for _, a := range Plan(w, f) {
		stale = stale || a.Kind == Stale
	}
	if !stale {
		t.Fatal("a base nobody acknowledged is stale")
	}
}

func TestInformationalAndBookkeepingKinds(t *testing.T) {
	for _, k := range []ActionKind{Skip, Conflict, Stale} {
		if !k.Informational() {
			t.Fatalf("%s", k)
		}
	}
	if !RecordHash.Bookkeeping() || RecordHash.Informational() || LinkPath.Informational() || LinkPath.Bookkeeping() {
		t.Fatal("kinds misclassified")
	}
}

func TestPlanNeverTouchesARecordedPathThatIsMidMerge(t *testing.T) {
	// Found by FuzzPlan: the profile no longer lists a recorded path, and the path is a
	// conflict-markers file. The "obsolete regular file" step would have saved and removed it.
	w := Want{}
	f := Facts{
		Paths:    map[string]PathFacts{"AGENTS.md": {FS: File, Unmerged: true}},
		Recorded: map[string]Record{"AGENTS.md": {Target: tgt}},
	}
	for _, a := range Plan(w, f) {
		if a.Path == "AGENTS.md" {
			t.Fatalf("planned %s on a path in the middle of a merge", a.Kind)
		}
	}
}

func TestPlanRemovesOurStrayLinksNextToASwappedPath(t *testing.T) {
	w, f := inSync()
	pf := f.Paths["AGENTS.md"]
	pf.StrayLinks = []string{".outfit-link~AGENTS.md", "AGENTS.md~cruft"}
	f.Paths["AGENTS.md"] = pf
	got := Plan(w, f)
	var removed []string
	for _, a := range got {
		if a.Kind == RemoveLink {
			removed = append(removed, a.Path)
		}
	}
	if len(removed) != 2 || removed[0] != ".outfit-link~AGENTS.md" || removed[1] != "AGENTS.md~cruft" {
		t.Fatalf("%v %v", kinds(got), removed)
	}
}
