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
	// Hard says the "link" is a hard link (a regular file that is the profile's own file under
	// another name), not a symlink; LinkTarget is then the profile file's path.
	Hard bool

	Tracked      bool   // the index has the path at stage 0
	SkipWorktree bool   // git's skip-worktree tag: git leaves the path alone
	Unmerged     bool   // the index has conflict stages for the path
	Mode         string // index mode, e.g. 100644

	MatchesIndex bool   // when FS == File: git says the file is clean (not modified against the index)
	FileHash     string // when FS == File and skip-worktree: SHA-256 of the file's content
	VariantHash  string // SHA-256 of the variant file; "" when it is missing
	// VariantExec says the variant file has an executable bit.
	VariantExec bool
	// Extra says the profile's file at this path may be linked although the repository does not
	// track it: it is below a directory the profile replaces and nothing in the way. ExtraWhy
	// says what is in the way when it is not ("" for a path that is not below a replaced one).
	Extra    bool
	ExtraWhy string
	// StrayLinks are OUR links standing where they do not belong, untracked: `<path>~cruft`
	// (older git renames what stands in the way of a conflict's markers file, the link,
	// to that name) and the temporary name a link is made under before it is renamed into
	// place (a kill between the two leaves it). Either would show up as an untracked
	// symlink that `git add -A` could commit.
	StrayLinks []string
	// StrayTarget is the link text (or, for a hard link, the profile file's path) the strays are ours by.
	StrayTarget string
	// StagedStrays are the StrayLinks that `git add -A` staged: removing them also takes them
	// off the index.
	StagedStrays []string
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
	// Extra marks a link below a replaced directory at a path the repository does not track.
	Extra bool
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
	// Ignore is, per directory the profile replaces now or replaced (a stale one is still
	// cleaned up), outfit's ignore file in it: what the self-ignoring `.gitignore` holds now.
	Ignore map[string]IgnoreFacts
	// Dirs describes each directory the active profile replaces (Want.ReplaceDirs).
	Dirs map[string]DirFacts
	// DirIssues are reasons a replace declaration cannot be honoured (a nested pair, a path that is
	// a tracked file or link, a submodule inside, too many files): each refuses the whole sync.
	DirIssues []string
	// UnhideStrays are, per directory that the patterns hide now and will no longer hide, the
	// regular files found on disk below it: git would leave them where they are and call that
	// success when the directory comes back.
	UnhideStrays map[string][]string
	// SameProfile says the active profile is the one the last sync recorded: a path it
	// no longer lists then went away from the profile itself (removed), not
	// because of a switch.
	SameProfile bool
}

// IgnoreFacts is the state of `<dir>/.gitignore`, the file that keeps the profile's own files
// below a replaced directory out of `git status` and `git add`: untracked, and ignoring itself.
type IgnoreFacts struct {
	Text    string // what it holds ("" when absent)
	Blocked string // why outfit may not write one ("" when it may): the team tracks one, or the file is not outfit's
}

// DirFacts is what sync learned about one replaced directory.
type DirFacts struct {
	Tracked int    // tracked files below it
	Digest  string // of their paths and blobs
	Seen    string // the digest recorded when the replacement was made or acknowledged
	Hidden  bool   // the patterns in force already hide it
	// Visible are tracked files below it that git has NOT hidden (a merge that touched them
	// clears the bit): the patterns must be applied again.
	Visible []string
	// Strays: see below.
	Strays []string
	// Unmerged says a merge conflict has files below it: nothing about it is touched.
	Unmerged bool
	// Strays are regular files standing at hidden team paths below it (written there by a person,
	// an editor or an agent while the directory was hidden): git treats such a path as "expected
	// outside the patterns" and may delete the file on a checkout, reset or pull without a word.
	// Staged and Dirty are changes below it, filled only when the directory is about to be hidden:
	// hiding would take them off disk (docs/git-behavior.md rows 35 and 40).
	Staged, Dirty []string
}

// Want is the desired state, derived from the active profile.
type Want struct {
	Swaps []Swap
	// ReplaceDirs are the directories the profile replaces as a whole: every tracked file below
	// them is hidden (Sparse says so), the profile's own files are linked in. Sorted, clean, not
	// nested in each other.
	ReplaceDirs []string
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
	WriteIgnore    ActionKind = "write-ignore"     // <dir>/.gitignore: the profile's own files below a replaced directory (Text "" removes it)
	SaveStray      ActionKind = "save-stray"       // a regular file at a hidden team path: copy it to the quarantine (it stays), exit 3
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
	// Extra (RemoveLink, LinkPath): the link is one of the profile's own files below Under, a replaced
	// directory: the directories it leaves empty go too, and it is a symlink, never a hard link.
	Extra bool
	Under string
	// Unstage (RemoveLink): the link is staged and HEAD does not have it: take it off the index too.
	Unstage bool
	// Adapted (RecordHash): one commit moved the base and the variant together, so the
	// swap's recorded base moves too.
	Adapted bool
}

// NeedsHuman reports whether any action means a person must look (exit 3).
func NeedsHuman(as []Action) bool {
	for _, a := range as {
		switch a.Kind {
		case QuarantineBoth, QuarantineFile, Conflict, SaveStray:
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
	for _, issue := range f.DirIssues {
		refuse = append(refuse, Action{Kind: Refuse, Reason: issue,
			Fix: "fix the `replace` lines of the profile (profile.config, [outfit \"profile\"] section), then: outfit sync"})
	}
	var skips, conflicts []Action
	var dirStale, strays []Action
	reapply := false
	for _, d := range w.ReplaceDirs {
		df := f.Dirs[d]
		switch {
		case df.Unmerged:
			conflicts = append(conflicts, Action{Kind: Conflict, Path: d,
				Reason: d + " has files in the middle of a merge conflict: outfit leaves the directory alone until it is resolved",
				Fix:    "resolve it and conclude the merge (git add --sparse <file> && git commit, or git rebase --continue), then: outfit sync"})
			continue
		case !df.Hidden && len(df.Staged) > 0:
			refuse = append(refuse, Action{Kind: Refuse, Path: d,
				Reason: "files below " + d + " are staged (" + strings.Join(firstN(df.Staged, 3), ", ") + "): replacing the directory would take them off disk, and the staged copy is the only one",
				Fix:    "git commit -m 'wip'   (or git restore --staged " + shq(d) + "), then: outfit sync"})
		case !df.Hidden && len(df.Dirty) > 0:
			refuse = append(refuse, Action{Kind: Refuse, Path: d,
				Reason: "files below " + d + " have uncommitted changes (" + strings.Join(firstN(df.Dirty, 3), ", ") + "): replacing the directory would leave the tree half-switched",
				Fix:    "git stash push -- " + shq(d) + "   (or commit them), then: outfit sync"})
		}
		if df.Hidden && len(df.Visible) > 0 {
			reapply = true
		}
		for _, sp := range df.Strays {
			strays = append(strays, Action{Kind: SaveStray, Path: sp,
				Reason: sp + " is a file at a path the team tracks inside " + d + ", which your profile replaces: git treats the path as hidden and may delete your file on a checkout, reset or pull. A copy is saved",
				Fix:    "move it out of " + d + " (or commit it somewhere else), then: outfit sync"})
		}
		if df.Seen != "" && df.Digest != "" && df.Seen != df.Digest {
			dirStale = append(dirStale, Action{Kind: Stale, Path: d,
				Reason: d + ": the team changed files below it (" + fmt.Sprint(df.Tracked) + " tracked now) since outfit replaced it; your profile's copy stands. When that is fine: outfit ack " + shq(d),
				Fix:    "git log --stat -- " + shq(d)})
		}
	}
	for _, d := range sortedDirKeys(f.UnhideStrays) {
		if len(f.UnhideStrays[d]) > 0 && !containsStr(w.ReplaceDirs, d) {
			refuse = append(refuse, Action{Kind: Refuse, Path: d,
				Reason: "regular files at paths the team tracks, which are not outfit's links, are in " + d + ", which the profile no longer replaces (" + strings.Join(firstN(f.UnhideStrays[d], 3), ", ") + "): git would leave them as they are, half-present, when the team's files come back",
				Fix:    "move them out (or commit them if they are meant to be the team's), then: outfit sync"})
		}
	}
	inConflict := map[string]bool{} // paths in the middle of a merge: nothing about them is touched
	extra := map[string]bool{}      // the profile's own files below a replaced directory, to be linked
	for _, p := range paths {
		pf := f.Paths[p]
		// The ORDER of these cases is part of the behaviour: a refusal must come before
		// any skip, or a switch could skip one file and change the tree around another.
		switch {
		case !pf.Tracked && !pf.Unmerged && underDir(p, w.ReplaceDirs) != "":
			// The profile replaces the directory this file is in and the repository does not track
			// it: the profile's own file, linked in as an "extra" when nothing is in the way.
			switch {
			case !pf.Extra:
				why := pf.ExtraWhy
				if why == "" {
					why = "something is in the way"
				}
				skips = append(skips, Action{Kind: Skip, Path: p, Reason: p + " is the profile's own file in " + underDir(p, w.ReplaceDirs) + ", which the profile replaces, but it is not linked: " + why})
				delete(want, p)
			case pf.FS == Dir || pf.FS == Other:
				skips = append(skips, Action{Kind: Skip, Path: p, Reason: p + " is a directory or something that is not a file or link, where the profile has a file: left alone"})
				delete(want, p)
			case pf.VariantHash == "":
				skips = append(skips, Action{Kind: Skip, Path: p, Reason: "the profile's file for " + p + " is missing"})
				delete(want, p)
			default:
				extra[p] = true
			}
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
	out = append(out, dirStale...)
	out = append(out, strays...)

	needSparse := w.Sparse != "" || len(want) > 0
	if needSparse && !f.ExpectFiles {
		out = append(out, Action{Kind: SetExpectFiles})
	}

	// Our stray links (`~cruft` after a merge, the temporary name of a link whose rename a kill
	// cut short): remove them at once (they are untracked, and `git add -A` would commit a symlink).
	for _, p := range sortedKeys(f.Paths) {
		for _, stray := range f.Paths[p].StrayLinks {
			out = append(out, Action{Kind: RemoveLink, Path: stray, Target: f.Paths[p].StrayTarget, Unstage: containsStr(f.Paths[p].StagedStrays, stray)})
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
	ignoreDirs := sortedIgnoreKeys(f.Ignore)
	for _, p := range obsolete {
		if rec := f.Recorded[p]; rec.Extra && !f.Paths[p].Tracked {
			// One of the profile's own files that the profile no longer has (or a link outfit made
			// that nothing records): the link goes, and the directories it leaves empty.
			switch f.Paths[p].FS {
			case Link:
				out = append(out, Action{Kind: RemoveLink, Path: p, Target: rec.Target, Extra: true, Under: underDir(p, ignoreDirs)})
			case File:
				out = append(out, Action{Kind: Skip, Path: p, Reason: p + " is a regular file where the profile's own file was linked, and the profile has no file for it now: left where it is"})
			}
			continue
		}
		switch pf := f.Paths[p]; pf.FS {
		case Link:
			a := Action{Kind: RemoveLink, Path: p, Target: f.Recorded[p].Target}
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
	case w.Sparse != "" && (!f.SparseOn || f.SparseText != w.Sparse || reapply):
		out = append(out, Action{Kind: WriteSparse, Text: w.Sparse})
	case w.Sparse == "" && f.SparseOn && managed(f.SparseText):
		out = append(out, Action{Kind: DisableSparse})
	}

	// The ignore file for the profile's own files is written after the links being taken away
	// and before the ones being made (git must never see a link it has not been told to ignore).
	linkStart := len(out)
	keep := map[string]bool{} // extras that stand (or will) and so are listed in the ignore file
	for _, p := range paths {
		s, ok := want[p]
		if !ok {
			continue
		}
		pf, rec := f.Paths[p], f.Recorded[p]
		_, had := f.Recorded[p]
		if extra[p] {
			out = append(out, planExtra(p, s, pf, rec, had, f.GitBusy, keep)...)
			continue
		}
		switch pf.FS {
		case Link:
			switch {
			case pf.LinkTarget != s.Target:
				out = append(out, Action{Kind: LinkPath, Path: p, Target: s.Target, Replaces: pf.Hard})
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
			case had && pf.FileHash != "" && pf.FileHash == rec.VariantHash && pf.VariantHash != rec.VariantHash:
				// A hard link the profile left: it was saved under its name as a new file (an
				// absorb from another worktree, an editor in the profile folder). This file is the
				// profile's OLD text, unchanged since linking, so the new text replaces it.
				out = append(out, Action{Kind: LinkPath, Path: p, Target: s.Target, Replaces: true})
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
	if ign := ignoreActions(w, f, paths, keep); len(ign) > 0 {
		out = append(out[:linkStart], append(ign, out[linkStart:]...)...)
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

// planExtra decides what one of the profile's own files below a replaced directory needs: a link
// where nothing is, the record when the link stands, the editor's save absorbed into the profile
// when the profile's copy is unchanged since linking. A regular file outfit did not put there is
// never replaced: a copy is saved and the file stays. keep gets the paths the ignore file lists.
func planExtra(p string, s Swap, pf PathFacts, rec Record, had bool, gitBusy string, keep map[string]bool) []Action {
	switch pf.FS {
	case Missing:
		keep[p] = true
		return []Action{{Kind: LinkPath, Path: p, Target: s.Target, Extra: true}}
	case Link:
		keep[p] = true
		if pf.LinkTarget != s.Target {
			return []Action{{Kind: LinkPath, Path: p, Target: s.Target, Extra: true}}
		}
		if !had || rec.VariantHash != pf.VariantHash || rec.Target != s.Target {
			return []Action{{Kind: RecordHash, Path: p, Target: s.Target}}
		}
	case File:
		switch {
		case gitBusy != "" && had && rec.Extra && rec.VariantHash == pf.VariantHash:
			// The profile's copy is tracked and git is in the middle of a rebase or merge: a
			// `--abort` resets it and would take the saved bytes with it (the same rule as a swap).
			keep[p] = true
			return []Action{{Kind: Skip, Path: p,
				Reason: p + " was replaced by a regular file (an editor's save), and git is in the middle of a " + gitBusy + ": the new bytes stay in that file and are copied into the profile once the " + gitBusy + " is over",
				Fix:    "finish or abort the " + gitBusy + ", then: outfit sync"}}
		case pf.FileHash != "" && pf.FileHash == pf.VariantHash, had && rec.Extra && rec.VariantHash == pf.VariantHash:
			// The same bytes, or an editor's atomic save over the link with the profile's copy
			// untouched since: the file is the newer copy.
			keep[p] = true
			return []Action{{Kind: Absorb, Path: p, Target: s.Target}}
		case had && rec.Extra:
			return []Action{{Kind: QuarantineBoth, Path: p,
				Reason: p + " was replaced by a regular file AND the profile's copy changed since outfit linked it: both are saved, the file is left where it is",
				Fix:    "compare them; put what you want to keep into the profile's copy, then remove " + shq(p) + " (or make the two identical) and run: outfit sync"}}
		default:
			return []Action{{Kind: SaveStray, Path: p,
				Reason: p + " is a regular file where the profile has its own file of that name: it is left where it is, and a copy is saved",
				Fix:    "move it away (or put its bytes in the profile's copy), then: outfit sync"}}
		}
	}
	return nil
}

// ignoreActions keeps `<dir>/.gitignore` equal to the files that stand below each directory: the
// ones the profile replaces now get their lines, a directory it no longer replaces loses the
// file. A directory where outfit may not write one (the team tracks a .gitignore there, or the file
// is somebody's) is left alone: no file of the profile's was linked there (see ExtraWhy).
func ignoreActions(w Want, f Facts, paths []string, keep map[string]bool) []Action {
	var out []Action
	for _, d := range sortedIgnoreKeys(f.Ignore) {
		cur := f.Ignore[d]
		if cur.Blocked != "" {
			if cur.Text != "" && OurIgnore(cur.Text) {
				// The team tracks a .gitignore here now, and ours is still standing at that path:
				// it goes (no file of the profile's is linked below this directory meanwhile).
				out = append(out, Action{Kind: WriteIgnore, Path: d})
			}
			continue
		}
		var files []string
		if containsStr(w.ReplaceDirs, d) {
			for _, p := range paths {
				if keep[p] && underDir(p, []string{d}) == d {
					files = append(files, p)
				}
			}
		}
		if text := IgnoreText(d, files); text != cur.Text {
			out = append(out, Action{Kind: WriteIgnore, Path: d, Text: text})
		}
	}
	return out
}

func sortedIgnoreKeys(m map[string]IgnoreFacts) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
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

func containsStr(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

func sortedDirKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// underDir returns the directory of dirs that p is inside, "" when none.
func underDir(p string, dirs []string) string {
	for _, d := range dirs {
		if strings.HasPrefix(p, d+"/") {
			return d
		}
	}
	return ""
}
