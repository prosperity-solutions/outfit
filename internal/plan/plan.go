// Package plan is the pure heart of sync (HANDOVER.md §3.5): Plan(Want, Facts)
// returns the actions that bring the worktree from what it is to what the active
// profile says it should be, and nothing else. It does no I/O, so every
// classification in the §3.5 table is a plain test.
package plan

import (
	"fmt"
	"sort"
	"strings"
)

// Kind is what a path is on disk.
type Kind int

// What lstat can find at a swapped path.
const (
	Missing Kind = iota
	Link
	File
	Dir
	Other
)

// Swap is one path the profile swaps, and the symlink text that stands in for it.
type Swap struct {
	Path   string
	Target string
}

// PathFacts is everything sync learned about one path: from lstat, from
// `git ls-files -s -t`, and from hashing the files involved.
type PathFacts struct {
	FS         Kind
	LinkTarget string // when FS == Link

	Tracked      bool   // the index has the path at stage 0
	SkipWorktree bool   // git's skip-worktree tag: git leaves the path alone
	Unmerged     bool   // the index has conflict stages for the path
	Mode         string // index mode, e.g. 100644

	MatchesIndex bool   // when FS == File: git says the file is clean (not modified against the index)
	FileHash     string // when FS == File and skip-worktree: SHA-256 of the file's content
	VariantHash  string // SHA-256 of the variant file; "" when it is missing
	// VariantExec says the variant file has an executable bit.
	VariantExec bool
}

// Record is what sync remembered about a path the last time it linked it.
type Record struct {
	Target      string
	VariantHash string
}

// Facts is the actual state of the worktree.
type Facts struct {
	SparseOn    bool   // core.sparseCheckout is true
	SparseText  string // info/sparse-checkout ("" when absent)
	ExpectFiles bool   // sparse.expectFilesOutsideOfPatterns is true
	ExcludeHas  bool   // .git/info/exclude already carries outfit's entry
	Paths       map[string]PathFacts
	Recorded    map[string]Record
	// StagedProfiles lists paths under .outfit/profiles that are staged but not
	// committed. Hiding a profile removes its files from disk; a staged-only file
	// has no other copy, and the next `git reset` would lose it.
	StagedProfiles []string
}

// Want is the desired state, derived from the active profile.
type Want struct {
	Swaps []Swap
	// Sparse is the managed sparse-checkout text, or "" when sparse checkout is not
	// needed (nothing swapped, nothing to hide).
	Sparse  string
	Exclude bool // .git/info/exclude should carry outfit's entry
	// HidesProfiles says Sparse hides the inactive committed profiles.
	HidesProfiles bool
}

// ActionKind names a step.
type ActionKind string

// The steps apply knows how to carry out.
const (
	SetExpectFiles ActionKind = "set-expect-files" // sparse.expectFilesOutsideOfPatterns=true
	WriteSparse    ActionKind = "write-sparse"     // git sparse-checkout set --no-cone --stdin
	DisableSparse  ActionKind = "disable-sparse"   // git sparse-checkout disable (and unset what we set)
	RemoveLink     ActionKind = "remove-link"      // a link the profile no longer wants
	LinkPath       ActionKind = "link"             // create or replace the link
	Absorb         ActionKind = "absorb"           // regular file replaced the link: copy it into the variant, relink
	QuarantineBoth ActionKind = "quarantine-both"  // file and variant both changed: save both, touch nothing, exit 3
	QuarantineFile ActionKind = "quarantine-file"  // an unwanted regular file: save it, then clear the path
	RecordHash     ActionKind = "record"           // state only: the variant changed through the link
	RepairSkip     ActionKind = "repair-skip"      // git update-index --skip-worktree: the bit was lost (a `git reset -- <path>`)
	AddExclude     ActionKind = "add-exclude"      // .git/info/exclude
	Conflict       ActionKind = "conflict"         // unmerged path: leave it alone, exit 3
	Skip           ActionKind = "skip"             // cannot swap this path; says why
	Refuse         ActionKind = "refuse"           // nothing is applied at all
)

// Action is one step.
type Action struct {
	Kind   ActionKind
	Path   string
	Target string   // link text, for LinkPath and Absorb
	Text   string   // sparse text, for WriteSparse
	Paths  []string // paths, for RepairSkip
	Reason string   // human text, for Skip, Refuse, Conflict and the quarantines
	Fix    string   // the exact command that fixes it, when there is one
	// Replaces says the path holds an UNTOUCHED copy of the committed file (checked
	// against the index), which LinkPath may remove to put the link there.
	Replaces bool
}

// NeedsHuman reports whether any action means a person must look (exit 3).
func NeedsHuman(as []Action) bool {
	for _, a := range as {
		switch a.Kind {
		case QuarantineBoth, QuarantineFile, Conflict:
			return true
		}
	}
	return false
}

// Refused returns the Refuse actions, which are all Plan returns when it refuses.
func Refused(as []Action) []Action {
	var out []Action
	for _, a := range as {
		if a.Kind == Refuse {
			out = append(out, a)
		}
	}
	return out
}

// Plan diffs Want against Facts. An empty result means in sync: nothing to do,
// nothing to write. When any Refuse is returned it is the ONLY thing returned:
// a refusal means nothing is applied.
func Plan(w Want, f Facts) []Action {
	want := map[string]Swap{}
	for _, s := range w.Swaps {
		want[s.Path] = s
	}
	paths := make([]string, 0, len(want))
	for p := range want {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	// --- refusals: decided before anything is touched ---
	var refuse []Action
	if f.SparseOn && !managed(f.SparseText) && (w.Sparse != "" || len(want) > 0) {
		refuse = append(refuse, Action{Kind: Refuse, Reason: "sparse checkout is already on and was not set up by outfit; outfit will not take over a sparse checkout it did not make",
			Fix: "git sparse-checkout disable   (then rerun outfit)"})
	}
	if w.HidesProfiles && len(f.StagedProfiles) > 0 && w.Sparse != f.SparseText {
		refuse = append(refuse, Action{Kind: Refuse,
			Reason: "profile files are staged but not committed (" + strings.Join(firstN(f.StagedProfiles, 3), ", ") + "): hiding the inactive profiles would take them off disk, and the staged copy is the only one",
			Fix:    "git commit -m 'feat: add profile'   (or: outfit author on, which keeps every profile on disk)"})
	}
	var skips, conflicts []Action
	for _, p := range paths {
		pf := f.Paths[p]
		// The ORDER of these cases is part of the behaviour: a refusal must come before
		// any skip, or a switch could skip one file and change the tree around another.
		switch {
		case !pf.Tracked && !pf.Unmerged:
			skips = append(skips, Action{Kind: Skip, Path: p, Reason: p + " is not tracked by git (a name must match the repository's exactly, case included), and outfit swaps only files the repository has"})
			delete(want, p)
		case pf.Unmerged:
			conflicts = append(conflicts, Action{Kind: Conflict, Path: p,
				Reason: p + " is in the middle of a merge conflict: outfit leaves it alone",
				Fix:    "resolve it, then: git add --sparse " + shq(p) + " && git commit, then outfit sync"})
			delete(want, p)
		case pf.Mode == "120000":
			refuse = append(refuse, Action{Kind: Refuse, Path: p, Reason: p + " is a symlink in the repository itself (or staged as one); outfit swaps regular files only",
				Fix: "git reset -q -- " + shq(p) + "   (if the link is only staged), then: outfit sync"})
		case pf.FS == Dir || pf.FS == Other:
			refuse = append(refuse, Action{Kind: Refuse, Path: p, Reason: p + " is a directory or something that is not a file or link, where the profile wants a file"})
		case pf.FS == File && !pf.SkipWorktree && !pf.MatchesIndex:
			// Before the missing-variant skip: right after a switch the new profile's
			// files are not on disk yet, and a refusal must come before anything changes.
			refuse = append(refuse, Action{Kind: Refuse, Path: p,
				Reason: p + " has uncommitted changes, and swapping it would hide them",
				Fix:    "git stash push -- " + shq(p) + "   (or commit it; do not `git stash pop` it back afterwards, put those edits in the profile's copy instead), then rerun"})
		case pf.VariantHash == "":
			skips = append(skips, Action{Kind: Skip, Path: p, Reason: "the profile's file for " + p + " is missing, so the base file stays"})
			delete(want, p)
		case pf.Mode == "100755" && !pf.VariantExec, pf.Mode == "100644" && pf.VariantExec:
			skips = append(skips, Action{Kind: Skip, Path: p,
				Reason: p + " and the profile's copy differ in the executable bit, which would break running it: the base file stays",
				Fix:    "give the profile's copy of " + shq(p) + " the same executable bit as the base file (chmod, then commit it), and run: outfit sync"})
			delete(want, p)
		}
	}
	if len(refuse) > 0 {
		return refuse
	}

	// --- steps ---
	var out []Action
	out = append(out, skips...)
	out = append(out, conflicts...)

	needSparse := w.Sparse != "" || len(want) > 0
	if needSparse && !f.ExpectFiles {
		out = append(out, Action{Kind: SetExpectFiles})
	}

	// Links the profile no longer wants go first, so the sparse change that follows
	// can bring the base file back.
	var obsolete []string
	for p := range f.Recorded {
		if _, still := want[p]; !still {
			obsolete = append(obsolete, p)
		}
	}
	sort.Strings(obsolete)
	for _, p := range obsolete {
		switch pf := f.Paths[p]; pf.FS {
		case Link:
			out = append(out, Action{Kind: RemoveLink, Path: p})
		case File:
			if pf.Tracked && !pf.SkipWorktree && pf.MatchesIndex {
				// Git already put the base file back (a sparse change that was cut short,
				// or a checkout): it is the committed copy, there is nothing to save.
				continue
			}
			out = append(out, Action{Kind: QuarantineFile, Path: p,
				Reason: p + " is a regular file where outfit's link used to be, and the profile no longer swaps it: a copy is saved before the base file is restored"})
		}
	}

	// Links whose skip-worktree bit was lost (sparse checkout switched off by hand,
	// `git reset -- <path>`): git must be told to leave them alone BEFORE the sparse
	// write, or it would see them as modified files and refuse to hide them.
	repaired := map[string]bool{}
	var lostLinks []string
	for _, p := range paths {
		if _, ok := want[p]; !ok {
			continue
		}
		if pf := f.Paths[p]; pf.Tracked && !pf.SkipWorktree && pf.FS == Link {
			lostLinks = append(lostLinks, p)
			repaired[p] = true
		}
	}
	if len(lostLinks) > 0 {
		out = append(out, Action{Kind: RepairSkip, Paths: lostLinks})
	}

	switch {
	case w.Sparse != "" && (!f.SparseOn || f.SparseText != w.Sparse):
		out = append(out, Action{Kind: WriteSparse, Text: w.Sparse})
	case w.Sparse == "" && f.SparseOn && managed(f.SparseText):
		out = append(out, Action{Kind: DisableSparse})
	}

	for _, p := range paths {
		s, ok := want[p]
		if !ok {
			continue
		}
		pf, rec := f.Paths[p], f.Recorded[p]
		_, had := f.Recorded[p]
		switch pf.FS {
		case Link:
			switch {
			case pf.LinkTarget != s.Target:
				out = append(out, Action{Kind: LinkPath, Path: p, Target: s.Target})
			case !had || rec.VariantHash != pf.VariantHash || rec.Target != s.Target:
				out = append(out, Action{Kind: RecordHash, Path: p, Target: s.Target})
			}
		case Missing:
			out = append(out, Action{Kind: LinkPath, Path: p, Target: s.Target})
		case File:
			switch {
			case !pf.SkipWorktree:
				// An untouched base file (the Refuse above caught a modified one): the
				// sparse change removes it, and where git did not, linking may.
				out = append(out, Action{Kind: LinkPath, Path: p, Target: s.Target, Replaces: true})
			case pf.FileHash != "" && pf.FileHash == pf.VariantHash:
				// The file and the profile's copy are the same bytes (a person resolved a
				// quarantine by making them equal): nothing to choose, just relink.
				out = append(out, Action{Kind: Absorb, Path: p, Target: s.Target})
			case had && rec.VariantHash == pf.VariantHash:
				// An editor's atomic save replaced the link. The variant is unchanged since
				// linking, so the file is the newer copy: absorb it, byte for byte.
				out = append(out, Action{Kind: Absorb, Path: p, Target: s.Target})
			default:
				out = append(out, Action{Kind: QuarantineBoth, Path: p,
					Reason: p + " was replaced by a regular file AND the profile's copy changed since outfit linked it: both are saved, the file is left where it is",
					Fix:    "compare them; put what you want to keep into the profile's copy, then remove " + shq(p) + " (or make the two identical) and run: outfit sync"})
			}
		}
	}
	// A swapped path whose skip-worktree bit is gone (git reset -- <path>, update-index)
	// would show as a type change and could be staged: put the bit back. A sparse
	// write sets the bits itself, so it only matters when none is planned.
	if !hasKind(out, WriteSparse) {
		var lost []string
		for _, p := range paths {
			if _, ok := want[p]; !ok || repaired[p] {
				continue
			}
			if pf := f.Paths[p]; pf.Tracked && !pf.SkipWorktree {
				lost = append(lost, p)
			}
		}
		if len(lost) > 0 {
			out = append(out, Action{Kind: RepairSkip, Paths: lost})
		}
	}
	if w.Exclude && !f.ExcludeHas {
		out = append(out, Action{Kind: AddExclude})
	}
	return out
}

func hasKind(as []Action, k ActionKind) bool {
	for _, a := range as {
		if a.Kind == k {
			return true
		}
	}
	return false
}

func managed(text string) bool { return strings.HasPrefix(text, Marker) }

// shq quotes a path for a fix command.
func shq(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`;&|<>()*?[]#~=%!{}") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Describe renders an action for logs and --json.
func (a Action) Describe() string {
	switch a.Kind {
	case LinkPath, Absorb, RecordHash:
		return fmt.Sprintf("%s %s -> %s", a.Kind, a.Path, a.Target)
	case WriteSparse:
		return string(a.Kind)
	}
	if a.Path != "" {
		return fmt.Sprintf("%s %s", a.Kind, a.Path)
	}
	return string(a.Kind)
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return append(append([]string(nil), s[:n]...), "…and more")
	}
	return s
}
