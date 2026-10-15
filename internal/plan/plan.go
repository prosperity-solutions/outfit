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
	// StrayLinks are OUR links standing where they do not belong, untracked: `<path>~cruft`
	// (older git renames what stands in the way of a conflict's markers file, the link,
	// to that name) and the temporary name a link is made under before it is renamed into
	// place (a kill between the two leaves it). Either would show up as an untracked
	// symlink that `git add -A` could commit.
	StrayLinks []string
	// VariantBlob is the blob id the index holds for the variant ("" when the profile
	// is not committed or the file is not tracked yet).
	VariantBlob string
	// SameCommit says the latest commit that touched the base file touched the
	// variant too (computed only when both moved since the last sync).
	SameCommit bool
	// BaseBlob is the blob id the index holds for the base file now ("" when git does
	// not know the path). A pull that changes the base moves it under a swap.
	BaseBlob string
}

// Record is what sync remembered about a path the last time it linked it.
type Record struct {
	Target      string
	VariantHash string
	// BaseBlob is the base blob the swap stands on (set at link time, moved by an ack or
	// an adapted commit): what "the base is different" is measured against. VariantBlob
	// is the variant's at the last sync.
	BaseBlob    string
	VariantBlob string
	// SeenBase is the base blob the last sync saw.
	SeenBase string
	// KnownBases are base blobs already acknowledged.
	KnownBases []string
}

// BaseKnown says the swap already stands on, or a person already acknowledged, this
// base blob.
func (r Record) BaseKnown(blob string) bool {
	if blob == r.BaseBlob {
		return true
	}
	for _, k := range r.KnownBases {
		if k == blob {
			return true
		}
	}
	return false
}

// Adapted says ONE commit changed the base and the profile's own file together: the
// profile's maintainer adapting it to the new base. The swap then stands on the new
// base. Anything looser re-baselines silently what nobody has looked at: a pull that
// brings an unrelated profile fix next to a base change, or a profile commit made
// alone after the base was already reported stale.
func Adapted(rec Record, pf PathFacts) bool {
	variantMoved := rec.VariantBlob != "" && pf.VariantBlob != "" && rec.VariantBlob != pf.VariantBlob
	return variantMoved && rec.SeenBase != "" && pf.BaseBlob != "" && rec.SeenBase != pf.BaseBlob && pf.SameCommit
}

// Informational kinds only say something; they never change anything.
func (k ActionKind) Informational() bool { return k == Skip || k == Conflict || k == Stale }

// Bookkeeping kinds only change what state.json records, never the worktree.
func (k ActionKind) Bookkeeping() bool { return k == RecordHash }

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
	// GitBusy names the git operation in progress in this worktree ("rebase", "merge",
	// "cherry-pick", "revert"), or "". git rewrites tracked files by itself during one (a
	// `rebase --abort` is a hard reset), so a tracked file outfit changed meanwhile can be
	// reverted behind its back: an editor's save is not absorbed into the profile's
	// (tracked) file until the operation is over.
	GitBusy string
	// SameProfile says the active profile is the one the last sync recorded: a path it
	// no longer lists then went away from the profile itself (removed), not
	// because of a switch.
	SameProfile bool
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
	Stale          ActionKind = "stale"            // the base file differs from when the swap was made: keep the swap, say so
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
	// Adapted (RecordHash): one commit moved the base and the variant together, so the
	// swap's recorded base moves too.
	Adapted bool
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
	listed := map[string]bool{} // what the profile lists, before any skip
	for _, s := range w.Swaps {
		want[s.Path] = s
		listed[s.Path] = true
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
	inConflict := map[string]bool{} // paths in the middle of a merge: nothing about them is touched
	for _, p := range paths {
		pf := f.Paths[p]
		// The ORDER of these cases is part of the behaviour: a refusal must come before
		// any skip, or a switch could skip one file and change the tree around another.
		switch {
		case !pf.Tracked && !pf.Unmerged:
			skips = append(skips, Action{Kind: Skip, Path: p, Reason: p + " is not tracked by git, and outfit swaps only files the repository has: a profile replaces tracked files, it cannot add one. Paths are relative to the repository root and must match the repository's spelling exactly, case included (apps/web/AGENTS.md, not web/AGENTS.md)"})
			delete(want, p)
		case pf.Unmerged:
			inConflict[p] = true
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

	// Our own link, moved aside by git for a merge's markers file: remove it at once (it
	// is untracked, and `git add -A` mid-resolution would commit a symlink).
	for _, p := range sortedKeys(f.Paths) {
		for _, stray := range f.Paths[p].StrayLinks {
			out = append(out, Action{Kind: RemoveLink, Path: stray})
		}
	}

	// Links the profile no longer wants go first, so the sparse change that follows
	// can bring the base file back.
	var obsolete []string
	for p := range f.Recorded {
		if _, still := want[p]; !still && !inConflict[p] && !f.Paths[p].Unmerged {
			obsolete = append(obsolete, p)
		}
	}
	sort.Strings(obsolete)
	for _, p := range obsolete {
		switch pf := f.Paths[p]; pf.FS {
		case Link:
			a := Action{Kind: RemoveLink, Path: p}
			if f.SameProfile && !listed[p] {
				a.Reason = p + " is no longer in the profile (removed, or an older branch?): the link is gone and the base file is back"
			}
			out = append(out, a)
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
			case !had || rec.VariantHash != pf.VariantHash || rec.Target != s.Target || (pf.VariantBlob != "" && rec.VariantBlob != pf.VariantBlob) || (pf.BaseBlob != "" && rec.SeenBase != pf.BaseBlob):
				out = append(out, Action{Kind: RecordHash, Path: p, Target: s.Target, Adapted: Adapted(rec, pf)})
			}
			// The swap stays; a person decides whether the new base needs a look.
			// Not when the same change moved the profile's file too: that is the profile's
			// maintainer adapting it to the new base (the record follows, see apply).
			if had && rec.BaseBlob != "" && pf.BaseBlob != "" && !rec.BaseKnown(pf.BaseBlob) && !Adapted(rec, pf) {
				out = append(out, Action{Kind: Stale, Path: p,
					Reason: p + ": the base file is different from when outfit swapped it (a pull, a merge or a checkout); the profile's copy is kept as it is. When the profile is fine as it is: outfit ack " + shq(p),
					Fix:    "outfit diff " + shq(p)})
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
			case had && rec.VariantHash == pf.VariantHash && f.GitBusy != "":
				// The same, but git is in the middle of a rebase/merge: it may reset the tracked
				// variant (a `rebase --abort`), taking the saved bytes with it. They stay where
				// they are, in the file the editor wrote, until git is done.
				out = append(out, Action{Kind: Skip, Path: p,
					Reason: p + " was replaced by a regular file (an editor's save), and git is in the middle of a " + f.GitBusy + ": the new bytes stay in that file and are copied into the profile once the " + f.GitBusy + " is over",
					Fix:    "finish or abort the " + f.GitBusy + ", then: outfit sync"})
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

// Quote quotes a path for a command a person pastes (the fixes outfit prints).
func Quote(s string) string { return shq(s) }

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

func sortedKeys(m map[string]PathFacts) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
