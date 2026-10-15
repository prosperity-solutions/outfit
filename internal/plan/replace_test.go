package plan

import (
	"strings"
	"testing"
)

// replacing builds a Want and Facts for one replaced directory `skills` with nothing swapped in it,
// the patterns in force already hiding it.
func replacing() (Want, Facts) {
	sparse := SparseText(SparseOptions{HideDirs: []string{"skills"}, HideProfiles: true, Show: []string{"mine"}})
	w := Want{ReplaceDirs: []string{"skills"}, Sparse: sparse, HidesProfiles: true, Exclude: true}
	f := Facts{
		SparseOn: true, SparseText: sparse, ExpectFiles: true, ExcludeHas: true,
		Paths:    map[string]PathFacts{},
		Recorded: map[string]Record{},
		Dirs:     map[string]DirFacts{"skills": {Tracked: 3, Digest: "d1", Seen: "d1", Hidden: true}},
	}
	return w, f
}

func TestReplacedDirectoryInSyncIsEmpty(t *testing.T) {
	w, f := replacing()
	if got := Plan(w, f); len(got) != 0 {
		t.Fatalf("an unchanged replaced directory plans %v", kinds(got))
	}
}

func TestReplacedDirectoryRefusesToBeHiddenOverStagedOrDirtyWork(t *testing.T) {
	for name, df := range map[string]DirFacts{
		"staged": {Tracked: 3, Digest: "d1", Staged: []string{"skills/b.md"}},
		"dirty":  {Tracked: 3, Digest: "d1", Dirty: []string{"skills/b.md"}},
	} {
		w, f := replacing()
		f.SparseText = SparseText(SparseOptions{HideProfiles: true, Show: []string{"mine"}}) // not hidden yet
		f.Dirs["skills"] = df
		got := Plan(w, f)
		if len(got) != 1 || got[0].Kind != Refuse || !strings.Contains(got[0].Reason, "skills/b.md") {
			t.Errorf("%s: %+v", name, got)
		}
	}
	// ... but not once it is hidden (what is below it is then not on disk to lose).
	w, f := replacing()
	df := f.Dirs["skills"]
	df.Staged = []string{"skills/b.md"}
	f.Dirs["skills"] = df
	if got := Plan(w, f); len(got) != 0 {
		t.Errorf("a directory that is hidden already must not refuse: %v", kinds(got))
	}
}

func TestStopReplacingRefusesOverStrayFiles(t *testing.T) {
	w, f := replacing()
	w.ReplaceDirs = nil
	w.Sparse = SparseText(SparseOptions{HideProfiles: true, Show: []string{"mine"}})
	f.UnhideStrays = map[string][]string{"skills": {"skills/new.md"}}
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != Refuse || !strings.Contains(got[0].Reason, "skills/new.md") {
		t.Fatalf("%+v", got)
	}
	f.UnhideStrays = map[string][]string{"skills": nil}
	if got := Plan(w, f); !has(got, WriteSparse, "") {
		t.Fatalf("with nothing in the way the patterns are rewritten: %v", kinds(got))
	}
}

func TestReplacedDirectoryInTheMiddleOfAMergeIsLeftAlone(t *testing.T) {
	w, f := replacing()
	f.Dirs["skills"] = DirFacts{Tracked: 3, Digest: "d1", Seen: "d1", Hidden: true, Unmerged: true, Visible: []string{"skills/b.md"}}
	got := Plan(w, f)
	if !has(got, Conflict, "skills") || has(got, WriteSparse, "") {
		t.Fatalf("%v", kinds(got))
	}
	if !NeedsHuman(got) {
		t.Fatal("a conflict below the directory needs a person")
	}
}

func TestFilesGitBroughtBackAreHiddenAgain(t *testing.T) {
	// A merge cleared skip-worktree on files below the directory: the same patterns must be applied again.
	w, f := replacing()
	f.Dirs["skills"] = DirFacts{Tracked: 3, Digest: "d1", Seen: "d1", Hidden: true, Visible: []string{"skills/b.md"}}
	got := Plan(w, f)
	if !has(got, WriteSparse, "") {
		t.Fatalf("%v", kinds(got))
	}
}

func TestTeamChangesBelowTheDirectoryAreToldOnce(t *testing.T) {
	w, f := replacing()
	f.Dirs["skills"] = DirFacts{Tracked: 4, Digest: "d2", Seen: "d1", Hidden: true}
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != Stale || got[0].Path != "skills" || !strings.Contains(got[0].Reason, "outfit ack skills") {
		t.Fatalf("%+v", got)
	}
	if actionableKinds(got) {
		t.Fatal("a stale note only says something")
	}
}

func TestUnsoundDeclarationsRefuse(t *testing.T) {
	w, f := replacing()
	f.DirIssues = []string{"replace = skills: the repository tracks skills as a file or link, not a directory"}
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != Refuse || !strings.Contains(got[0].Fix, "profile.config") {
		t.Fatalf("%+v", got)
	}
}

func actionableKinds(as []Action) bool {
	for _, a := range as {
		if !a.Kind.Informational() {
			return true
		}
	}
	return false
}

func TestSparseTextHidesADirectoryOnceAndDropsCoveredPaths(t *testing.T) {
	got := SparseText(SparseOptions{HideDirs: []string{"b dir", "skills", "skills"}, Hidden: []string{"AGENTS.md", "skills/a.md", "b dir/x"}})
	want := Marker + "\n/*\n!/b dir/\n!/skills/\n!/AGENTS.md\n"
	if got != want {
		t.Fatalf("%q != %q", got, want)
	}
	o, ok := ParseSparse(got)
	if !ok || len(o.HideDirs) != 2 || len(o.Hidden) != 1 || o.Hidden[0] != "AGENTS.md" {
		t.Fatalf("%+v %v", o, ok)
	}
	if SparseText(o) != got {
		t.Fatal("not a fixed point")
	}
}

// extraIn adds one of the profile's own files (untracked, below skills/) to a replacing() world.
func extraIn(p string, pf PathFacts) (Want, Facts) {
	w, f := replacing()
	w.Swaps = append(w.Swaps, Swap{Path: p, Target: "../.outfit/profiles/mine/" + p})
	pf.Extra = true
	if pf.VariantHash == "" {
		pf.VariantHash = "v1"
	}
	f.Paths[p] = pf
	f.Ignore = map[string]IgnoreFacts{"skills": {}}
	return w, f
}

func TestAnExtraIsLinkedAndListedInTheIgnoreFileFirst(t *testing.T) {
	w, f := extraIn("skills/new.md", PathFacts{FS: Missing})
	got := Plan(w, f)
	if len(got) != 2 || got[0].Kind != WriteIgnore || got[1].Kind != LinkPath {
		t.Fatalf("the ignore file must come before the link: %v", kinds(got))
	}
	if got[0].Path != "skills" || !strings.Contains(got[0].Text, "/new.md\n") || !OurIgnore(got[0].Text) {
		t.Errorf("ignore text: %q", got[0].Text)
	}
}

func TestAnExtraThatStandsPlansNothing(t *testing.T) {
	w, f := extraIn("skills/new.md", PathFacts{FS: Link, LinkTarget: "../.outfit/profiles/mine/skills/new.md"})
	f.Recorded["skills/new.md"] = Record{Target: "../.outfit/profiles/mine/skills/new.md", VariantHash: "v1", Extra: true}
	f.Ignore["skills"] = IgnoreFacts{Text: IgnoreText("skills", []string{"skills/new.md"})}
	if got := Plan(w, f); len(got) != 0 {
		t.Fatalf("%v", kinds(got))
	}
}

func TestAnEditorsSaveOverAnExtraIsAbsorbedOnlyWhenTheProfilesCopyIsUnchanged(t *testing.T) {
	target := "../.outfit/profiles/mine/skills/new.md"
	w, f := extraIn("skills/new.md", PathFacts{FS: File, VariantHash: "v1"})
	f.Recorded["skills/new.md"] = Record{Target: target, VariantHash: "v1", Extra: true}
	f.Ignore["skills"] = IgnoreFacts{Text: IgnoreText("skills", []string{"skills/new.md"})}
	if got := Plan(w, f); len(got) != 1 || got[0].Kind != Absorb {
		t.Fatalf("unchanged variant: %v", kinds(got))
	}
	f.Recorded["skills/new.md"] = Record{Target: target, VariantHash: "older", Extra: true}
	if got := Plan(w, f); !hasKind(got, QuarantineBoth) || hasKind(got, Absorb) {
		t.Fatalf("changed variant: %v", kinds(got))
	}
}

func TestARegularFileOutfitDidNotMakeIsNeverReplacedByAnExtra(t *testing.T) {
	w, f := extraIn("skills/new.md", PathFacts{FS: File, FileHash: "mine"})
	got := Plan(w, f)
	if !hasKind(got, SaveStray) || hasKind(got, LinkPath) || hasKind(got, Absorb) || hasKind(got, WriteIgnore) {
		t.Fatalf("%v", kinds(got))
	}
}

func TestAnExtraInTheWayIsSkippedWithItsReasonAndNeverListed(t *testing.T) {
	w, f := extraIn("skills/new.md", PathFacts{FS: Missing})
	pf := f.Paths["skills/new.md"]
	pf.Extra, pf.ExtraWhy = false, "the team tracks a file at skills, where a directory would have to stand"
	f.Paths["skills/new.md"] = pf
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != Skip || !strings.Contains(got[0].Reason, "where a directory would have to stand") {
		t.Fatalf("%+v", got)
	}
}

func TestAnExtraTheProfileDroppedLosesItsLinkAndTheEmptyFolders(t *testing.T) {
	w, f := replacing()
	f.Paths["skills/deep/x.md"] = PathFacts{FS: Link, LinkTarget: "../../.outfit/profiles/mine/skills/deep/x.md"}
	f.Recorded["skills/deep/x.md"] = Record{Target: "../../.outfit/profiles/mine/skills/deep/x.md", VariantHash: "v1", Extra: true}
	f.Ignore = map[string]IgnoreFacts{"skills": {Text: IgnoreText("skills", []string{"skills/deep/x.md"})}}
	got := Plan(w, f)
	if len(got) != 2 || got[0].Kind != RemoveLink || !got[0].Extra || got[0].Under != "skills" || got[1].Kind != WriteIgnore || got[1].Text != "" {
		t.Fatalf("%+v", got)
	}
}

func TestLeavingTheProfileRemovesTheIgnoreFileAfterTheLinks(t *testing.T) {
	w, f := replacing()
	w.ReplaceDirs, w.Swaps = nil, nil
	w.Sparse = ""
	f.SparseOn = true
	f.Paths["skills/new.md"] = PathFacts{FS: Link, LinkTarget: "x"}
	f.Recorded["skills/new.md"] = Record{Target: "x", Extra: true}
	f.Ignore = map[string]IgnoreFacts{"skills": {Text: IgnoreText("skills", []string{"skills/new.md"})}}
	got := Plan(w, f)
	var order []string
	for _, a := range got {
		order = append(order, string(a.Kind))
	}
	li, ii := indexOf(order, string(RemoveLink)), indexOf(order, string(WriteIgnore))
	if li < 0 || ii < 0 || li > ii {
		t.Fatalf("links go before the ignore file: %v", order)
	}
}

func TestABlockedIgnoreFileIsLeftAlone(t *testing.T) {
	w, f := extraIn("skills/new.md", PathFacts{FS: Missing})
	f.Ignore["skills"] = IgnoreFacts{Blocked: "the team tracks skills/.gitignore"}
	pf := f.Paths["skills/new.md"]
	pf.Extra, pf.ExtraWhy = false, "the team tracks skills/.gitignore"
	f.Paths["skills/new.md"] = pf
	if got := Plan(w, f); hasKind(got, WriteIgnore) || hasKind(got, LinkPath) {
		t.Fatalf("%v", kinds(got))
	}
}

func TestIgnoreTextIsAPureFunctionOfItsInputs(t *testing.T) {
	a := IgnoreText("skills", []string{"skills/b.md", "skills/a b.md", "skills/#x", "skills/deep/x.md", "skills/b.md"})
	b := IgnoreText("skills", []string{"skills/#x", "skills/deep/x.md", "skills/a b.md", "skills/b.md"})
	if a != b || !OurIgnore(a) {
		t.Fatalf("%q != %q", a, b)
	}
	for _, want := range []string{"/.gitignore\n", "/a b.md\n", "/\\#x\n", "/deep/x.md\n", "/b.md\n"} {
		if !strings.Contains(a, want) {
			t.Errorf("missing %q in %q", want, a)
		}
	}
	if IgnoreText("skills", nil) != "" || OurIgnore("# mine\n/.gitignore\n") {
		t.Error("no files is no file, and only outfit's marker line makes it outfit's")
	}
}

func indexOf(s []string, x string) int {
	for i, v := range s {
		if v == x {
			return i
		}
	}
	return -1
}
