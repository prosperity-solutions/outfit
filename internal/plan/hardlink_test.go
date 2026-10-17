package plan

import "testing"

// A hard link is a "link" to the planner (gather says so with FS == Link and Hard); only what it is
// replaced by and how it is taken away differ.

func TestAHardLinkInSyncPlansNothing(t *testing.T) {
	w, f := inSync()
	pf := f.Paths["AGENTS.md"]
	pf.Hard = true
	f.Paths["AGENTS.md"] = pf
	if got := Plan(w, f); len(got) != 0 {
		t.Fatalf("%v", kinds(got))
	}
}

func TestSwitchingProfileReplacesAHardLinkAndNamesTheOldTarget(t *testing.T) {
	w, f := inSync()
	pf := f.Paths["AGENTS.md"]
	pf.Hard, pf.LinkTarget = true, "/g/outfit/profiles/old/AGENTS.md"
	f.Paths["AGENTS.md"] = pf
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != LinkPath || !got[0].Replaces || got[0].Target != tgt {
		t.Fatalf("a hard link of another profile's file is ours to replace: %+v", got)
	}
	// A profile that no longer swaps the path: the link goes, told by the old target.
	w.Swaps = nil
	f.Recorded["AGENTS.md"] = Record{Target: "/g/outfit/profiles/old/AGENTS.md", VariantHash: "h1"}
	got = Plan(w, f)
	if !hasKind(got, RemoveLink) {
		t.Fatalf("%v", kinds(got))
	}
	for _, a := range got {
		if a.Kind == RemoveLink && a.Target != "/g/outfit/profiles/old/AGENTS.md" {
			t.Fatalf("RemoveLink must carry the recorded target (how a hard link is told from a stranger's file): %+v", a)
		}
	}
}

func TestAStaleCopyOfTheProfileIsReplacedNotQuarantined(t *testing.T) {
	// The profile was saved under its name as a new file (an absorb from another worktree): this
	// worktree's file is the profile's OLD text, unchanged since linking.
	w, f := inSync()
	f.Paths["AGENTS.md"] = PathFacts{FS: File, Tracked: true, SkipWorktree: true, Mode: "100644", FileHash: "old", VariantHash: "new"}
	f.Recorded["AGENTS.md"] = Record{Target: tgt, VariantHash: "old"}
	got := Plan(w, f)
	if len(got) != 1 || got[0].Kind != LinkPath || !got[0].Replaces {
		t.Fatalf("%v", kinds(got))
	}
	// ... but a file that is neither the old text nor the new one is the person's: both saved.
	pf := f.Paths["AGENTS.md"]
	pf.FileHash = "edited"
	f.Paths["AGENTS.md"] = pf
	if got := Plan(w, f); !hasKind(got, QuarantineBoth) || hasKind(got, LinkPath) {
		t.Fatalf("%v", kinds(got))
	}
}

func TestAnExtraStaysASymlink(t *testing.T) {
	w, f := extraIn("skills/new.md", PathFacts{FS: Missing})
	for _, a := range Plan(w, f) {
		if a.Kind == LinkPath && !a.Extra {
			t.Fatalf("the profile's own files below a replaced directory are never hard-linked: %+v", a)
		}
	}
}
