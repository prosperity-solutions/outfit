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
