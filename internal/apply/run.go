package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// Result is what a sync did and found.
type Result struct {
	// Actions are the steps that were planned, informational ones included.
	Actions []plan.Action `json:"-"`
	// Lasting is the part of Notes that stays true after a later pass: what was
	// quarantined, a link removed because the profile no longer has its file, and what
	// gather noticed in the state it read (an ignored record, an oversize profile). A
	// skip note (a file not on disk yet) does not.
	Lasting []string `json:"-"`
	// Done holds the steps earlier passes of one converge carried out: the last
	// pass of a switch plans nothing, but the switch happened.
	Done []plan.Action `json:"-"`
	// Changed reports that something was written.
	Changed bool
	// Refused holds the reasons nothing was applied (exit 3 with fixes).
	Refused []plan.Action
	// NeedsHuman means a conflict or a quarantine: a person must look (exit 3).
	NeedsHuman bool
	// Notes are lines for stderr: skipped paths, conflicts, quarantines.
	Notes []string
	// Quarantine is the directory copies were saved to, when there was one.
	Quarantine string
}

// Run reconciles the worktree with the active profile: plan without the lock, and
// only when there is something to do take the lock, plan again (the world may have
// moved while it was held by someone else) and apply. The no-op path writes nothing
// and takes no lock.
func Run(ctx context.Context, in Input) (*Result, error) {
	st, err := readState(in)
	if err != nil {
		return nil, err
	}
	res, acts, err := in.planOnce(ctx, st)
	if err != nil || len(res.Refused) > 0 || !actionable(acts) {
		return res, err
	}

	lock, err := AcquireLock(in.W.GitDir)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	if st, err = readState(in); err != nil {
		return nil, err
	}
	// Author mode is a setting `outfit author` writes to state.json; one written while
	// this sync waited for the lock must win over the value it was started with.
	in.Author = st.Author
	res, acts, err = in.planOnce(ctx, st)
	if err != nil || len(res.Refused) > 0 || !actionable(acts) {
		return res, err
	}
	res, err = in.execute(ctx, st, acts, res)
	if res != nil && res.Changed {
		res.Done = append(res.Done, acts...)
	}
	return res, err
}

func readState(in Input) (*state.State, error) {
	st, err := state.Read(in.W.GitDir)
	if errors.Is(err, state.ErrNone) {
		return &state.State{}, nil
	}
	return st, err
}

// planOnce gathers and plans, and classifies the outcome into a Result.
func (in Input) planOnce(ctx context.Context, st *state.State) (*Result, []plan.Action, error) {
	want, facts, g, err := in.gather(ctx, st)
	if err != nil {
		return nil, nil, err
	}
	acts := plan.Plan(want, facts)
	// What gather itself noticed (an ignored record, an oversize profile) is true of
	// the state it read, which a later pass will have already repaired: keep it.
	res := &Result{Actions: acts, Notes: append([]string(nil), g.messages...), Lasting: append([]string(nil), g.messages...)}
	res.Refused = plan.Refused(acts)
	res.NeedsHuman = plan.NeedsHuman(acts)
	for _, a := range acts {
		switch a.Kind {
		case plan.Skip, plan.Conflict, plan.QuarantineBoth, plan.QuarantineFile, plan.Stale, plan.RemoveLink:
			if a.Kind == plan.RemoveLink && a.Reason == "" {
				continue
			}
			line := a.Reason
			if a.Fix != "" {
				line += "\n  fix: " + a.Fix
			}
			res.Notes = append(res.Notes, line)
			if a.Kind == plan.QuarantineBoth || a.Kind == plan.QuarantineFile || a.Kind == plan.RemoveLink {
				res.Lasting = append(res.Lasting, line)
			}
		}
	}
	return res, acts, nil
}

// actionable reports whether anything has to be written or run.
func actionable(acts []plan.Action) bool {
	for _, a := range acts {
		if !a.Kind.Informational() {
			return true
		}
	}
	return false
}

func (in Input) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

// execute carries the plan out, in order, then records the new state. A failure
// stops at the failing step: what was done stays done (every step is individually
// atomic), and the next sync, being level-triggered, converges from there.
func (in Input) execute(ctx context.Context, st *state.State, acts []plan.Action, res *Result) (*Result, error) {
	next := *st
	quarantined := map[string]bool{}
	adapted := map[string]bool{} // paths whose recorded base moves (plan.Adapted)
	for _, a := range acts {
		if a.Kind == plan.RecordHash && a.Adapted {
			adapted[a.Path] = true
		}
	}

	stepErr := func() error {
		for _, a := range acts {
			switch a.Kind {
			case plan.Skip, plan.Conflict, plan.RecordHash, plan.Stale:
				// (plan.ActionKind.Informational / Bookkeeping: nothing to run; RecordHash only changes the state written below.
			case plan.SetExpectFiles:
				if _, err := in.Git.Write(ctx, "config", "--worktree", "sparse.expectFilesOutsideOfPatterns", "true"); err != nil {
					return fmt.Errorf("cannot set sparse.expectFilesOutsideOfPatterns: %w", err)
				}
				next.ExpectFilesSet = true
				res.Changed = true
			case plan.RemoveLink:
				p := in.abs(a.Path)
				if !realParents(in.W.WorkTree, a.Path) {
					return fmt.Errorf("%s is below a symlinked directory: refusing to act through it", a.Path)
				}
				if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
					if err := os.Remove(p); err != nil {
						return err
					}
					res.Changed = true
				}
			case plan.QuarantineFile:
				if !realParents(in.W.WorkTree, a.Path) {
					return fmt.Errorf("%s is below a symlinked directory: refusing to act through it", a.Path)
				}
				if _, err := in.quarantine(res, a.Path, true, false); err != nil {
					return err
				}
				// The copy is saved (now or by an earlier run): the file may go.
				if err := os.Remove(in.abs(a.Path)); err != nil {
					return err
				}
				res.Changed = true
			case plan.WriteSparse:
				if err := in.writeSparse(ctx, a.Text); err != nil {
					return err
				}
				next.SparseSet = true
				res.Changed = true
			case plan.DisableSparse:
				if err := in.disableSparse(ctx, &next); err != nil {
					return err
				}
				res.Changed = true
			case plan.LinkPath:
				if err := in.link(a.Path, a.Target, a.Replaces); err != nil {
					return err
				}
				res.Changed = true
			case plan.Absorb:
				if err := in.absorb(a.Path, a.Target); err != nil {
					return err
				}
				res.Changed = true
			case plan.QuarantineBoth:
				created, err := in.quarantine(res, a.Path, true, true)
				if err != nil {
					return err
				}
				quarantined[a.Path] = true
				if created {
					res.Changed = true
				}
			case plan.RepairSkip:
				args := append([]string{"update-index", "--skip-worktree", "--"}, a.Paths...)
				if _, err := in.Git.Write(ctx, args...); err != nil {
					return fmt.Errorf("cannot restore git's skip-worktree bit on %s: %w", strings.Join(a.Paths, ", "), err)
				}
				res.Changed = true
			case plan.AddExclude:
				if err := addExclude(filepath.Join(in.W.CommonDir, "info", "exclude")); err != nil {
					return err
				}
				res.Changed = true
			}
		}
		return nil
	}()

	// The new state records what is REALLY there now: every wanted path that is a
	// link to its variant, with what it is made of. A quarantined path keeps its OLD
	// record, so the next sync still finds the mismatch instead of absorbing over it.
	// Also after a failed step: a record that still names a link the failed step (or
	// an earlier killed run) already removed would make the next sync act on a
	// world that is gone.
	want, facts, g, err := in.gather(ctx, &state.State{})
	if err != nil {
		return res, errors.Join(stepErr, err)
	}
	next.Swaps = nil
	for _, sw := range want.Swaps {
		pf := facts.Paths[sw.Path]
		switch {
		case quarantined[sw.Path] || pf.Unmerged:
			// (a path in the middle of a merge keeps its record too: the base it stood on is
			// what the merge's result is compared with afterwards)
			for _, old := range st.Swaps {
				if old.Path == sw.Path {
					next.Swaps = append(next.Swaps, old)
				}
			}
		case pf.FS == plan.Link && pf.LinkTarget == sw.Target && pf.Tracked && pf.VariantHash != "":
			// The base blob the swap stands on: the first link sets it, an `ack` or an adapted
			// commit (plan.Adapted, decided by the plan) moves it, an ordinary sync never
			// does, or "stale" would clear itself the next time anything else changed.
			base := g.blobs[sw.Path]
			var known []string
			for _, old := range st.Swaps {
				if old.Path == sw.Path && old.Target == sw.Target && old.BaseBlob != "" {
					known = old.KnownBases
					if !adapted[sw.Path] { // the plan decided (plan.Adapted): one commit moved base and variant
						base = old.BaseBlob
					}
				}
			}
			next.Swaps = append(next.Swaps, state.Swap{Path: sw.Path, Target: sw.Target, VariantHash: pf.VariantHash, VariantBlob: pf.VariantBlob, BaseBlob: base, SeenBase: g.blobs[sw.Path], KnownBases: known})
		}
	}
	sort.Slice(next.Swaps, func(i, j int) bool { return next.Swaps[i].Path < next.Swaps[j].Path })
	next.Profile = ""
	if in.Profile != nil {
		next.Profile = in.Profile.Name
	}
	next.Author = in.Author
	next.CommittedProfiles = in.CommittedProfiles
	changed, werr := state.Write(in.W.GitDir, &next)
	if werr == nil && changed {
		res.Changed = true
	}
	return res, errors.Join(stepErr, werr)
}

// writeSparse hands the managed patterns to git, which updates the pattern file,
// the skip-worktree bits and the working tree together (HANDOVER.md §3.4.5). A
// path git could not update (it has uncommitted changes) fails the step loudly: a
// half-switched tree is the one state outfit must never leave quietly.
func (in Input) writeSparse(ctx context.Context, text string) error {
	res, err := in.Git.WriteStdin(ctx, []byte(text), "sparse-checkout", "set", "--no-cone", "--stdin")
	if err != nil {
		return fmt.Errorf("git sparse-checkout set failed: %w", err)
	}
	if strings.Contains(string(res.Stderr), "not up to date") || strings.Contains(string(res.Stderr), "left despite sparse patterns") {
		return fmt.Errorf("git left files in place because they have uncommitted changes, so the switch is half done: %s\n  fix: commit those changes, or put them in the profile's copy, then: outfit sync", strings.TrimSpace(string(res.Stderr)))
	}
	b, err := readInside(in.W.GitDir, "info/sparse-checkout")
	if err != nil {
		return fmt.Errorf("git did not write the sparse-checkout file: %w", err)
	}
	if string(b) != text {
		return fmt.Errorf("git rewrote outfit's sparse-checkout patterns (this git is not compatible): %q", string(b))
	}
	return nil
}

// disableSparse restores everything sparse checkout hid and removes the settings
// outfit added, and only those.
func (in Input) disableSparse(ctx context.Context, st *state.State) error {
	if _, err := in.Git.Write(ctx, "sparse-checkout", "disable"); err != nil {
		return fmt.Errorf("git sparse-checkout disable failed: %w", err)
	}
	unsetWorktree(ctx, in, "core.sparseCheckout", "core.sparseCheckoutCone")
	// `git sparse-checkout disable` leaves index.sparse=false behind; a true value
	// is somebody's sparse index and stays.
	if res, err := in.Git.Read(ctx, "config", "--worktree", "--get", "index.sparse"); err == nil && strings.TrimSpace(string(res.Stdout)) == "false" {
		unsetWorktree(ctx, in, "index.sparse")
	}
	if st.ExpectFilesSet {
		unsetWorktree(ctx, in, "sparse.expectFilesOutsideOfPatterns")
		st.ExpectFilesSet = false
	}
	st.SparseSet = false
	_ = os.Remove(filepath.Join(in.W.GitDir, "info", "sparse-checkout"))
	return nil
}

func unsetWorktree(ctx context.Context, in Input, keys ...string) {
	for _, k := range keys {
		// Exit 5 means "not set": fine.
		_, _ = in.Git.Write(ctx, "config", "--worktree", "--unset-all", k)
	}
}

// mkdirAllLike makes the directories above rel (below root) with the permissions of
// the repository's own top directory (unlike git, which applies the umask: a person
// who keeps their checkout private keeps the new folders private); os.Root keeps every one of them inside root.
func mkdirAllLike(root, rel string) error {
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	dir := pathpkg.Dir(rel)
	if dir == "." {
		return nil
	}
	mode := os.FileMode(0o750)
	if fi, err := r.Stat("."); err == nil {
		mode = fi.Mode().Perm()
	}
	return r.MkdirAll(dir, mode)
}

// link puts the symlink for path in place: parent directories first (sparse
// checkout removes a directory whose only file was hidden, verified), the link
// made under a temporary name and renamed over the target so there is never a
// moment with neither a file nor a link. An existing regular file is removed only
// when the plan proved it an untouched copy of the committed file.
func (in Input) link(path, target string, replaces bool) error {
	if !realParents(in.W.WorkTree, path) {
		return fmt.Errorf("%s is below a symlinked directory: refusing to link through it", path)
	}
	abs := in.abs(path)
	if err := mkdirAllLike(in.W.WorkTree, path); err != nil {
		return err
	}
	if fi, err := os.Lstat(abs); err == nil {
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
		case fi.Mode().IsRegular() && replaces:
		default:
			return fmt.Errorf("%s exists and is not outfit's to replace", path)
		}
	}
	tmp := filepath.Join(filepath.Dir(abs), fmt.Sprintf(".outfit-link-%d-%d", os.Getpid(), time.Now().UnixNano()))
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// absorb copies the regular file that replaced a link into the variant, byte for
// byte and atomically, then relinks. The caller proved the variant unchanged since
// linking, so nothing of the variant's is lost.
func (in Input) absorb(path, target string) error {
	if !realParents(in.Profile.Dir, filepath.ToSlash(path)) {
		return fmt.Errorf("the profile's directory for %s contains a symlink: not copying bytes through it", path)
	}
	abs := in.abs(path)
	fi, err := os.Lstat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%s is no longer a regular file; nothing was absorbed", path)
	}
	if fi.Size() > maxFile {
		return fmt.Errorf("%s is larger than %d bytes; it was not absorbed", path, maxFile)
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	mode := fs.FileMode(0o644)
	if vi, err := os.Lstat(in.variantPath(path)); err == nil {
		mode = vi.Mode().Perm()
	}
	if err := state.WriteFileAtomic(in.variantPath(path), b, mode); err != nil {
		return fmt.Errorf("cannot save %s into the profile: %w", path, err)
	}
	return in.link(path, target, true)
}

// quarantine saves copies under <git-dir>/outfit/quarantine/<timestamp>-<key>/ : the
// file at path under "file/", and with withVariant the profile's copy under
// "variant/". It never moves or deletes anything itself. The key is a hash of what
// is being saved, so a sync that finds the same conflict again reuses the directory
// instead of piling up copies. created reports whether anything was written.
func (in Input) quarantine(res *Result, path string, file, withVariant bool) (created bool, err error) {
	fileHash := ""
	if file {
		fileHash = hashFile(in.abs(path))
	}
	variantHash := ""
	if withVariant && in.Profile != nil {
		variantHash = hashFile(in.variantPath(path))
	}
	sum := sha256.Sum256([]byte(path + "\x00" + fileHash + "\x00" + variantHash))
	key := hex.EncodeToString(sum[:])[:12]
	root := filepath.Join(in.W.GitDir, "outfit", "quarantine")
	res.Quarantine = filepath.Join(root, in.now().UTC().Format("20060102T150405.000000Z")+"-"+key)
	if prev, _ := filepath.Glob(filepath.Join(root, "*-"+key)); len(prev) > 0 {
		// An earlier run made this directory. It counts as "already saved" only if the
		// copies are really in it and are what is about to be removed or kept: a run
		// killed half way leaves a directory with nothing (or half a copy) inside.
		res.Quarantine = prev[0]
		complete := true
		if file && hashFile(filepath.Join(prev[0], "file", filepath.FromSlash(path))) != fileHash {
			complete = false
		}
		if withVariant && in.Profile != nil && hashFile(filepath.Join(prev[0], "variant", filepath.FromSlash(path))) != variantHash {
			complete = false
		}
		if complete {
			return false, nil
		}
	}
	copyTo := func(src, kind string) error {
		fi, err := os.Lstat(src)
		if err != nil || !fi.Mode().IsRegular() {
			return nil // nothing there to save
		}
		if fi.Size() > maxFile {
			return fmt.Errorf("%s is larger than %d bytes; it cannot be quarantined, so it was not touched", src, maxFile)
		}
		b, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return state.WriteFileAtomic(filepath.Join(res.Quarantine, kind, filepath.FromSlash(path)), b, 0o644)
	}
	if file {
		if err := copyTo(in.abs(path), "file"); err != nil {
			return false, err
		}
	}
	if withVariant && in.Profile != nil {
		if err := copyTo(in.variantPath(path), "variant"); err != nil {
			return false, err
		}
	}
	return true, nil
}

// addExclude appends outfit's entry to .git/info/exclude, keeping every line a
// person wrote.
func addExclude(path string) error { return AddExcludeEntry(path, excludeComment, excludeLine) }

// AddExcludeEntry appends a comment line and a pattern line to an exclude file.
func AddExcludeEntry(path, comment, line string) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(old)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += comment + "\n" + line + "\n"
	return state.WriteFileAtomic(path, []byte(text), 0o644)
}

// RemoveExclude takes outfit's own .outfit entry out of .git/info/exclude: the comment
// line and the pattern line after it, never one a person wrote.
func RemoveExclude(path string) (bool, error) {
	return RemoveExcludeEntry(path, excludeComment, excludeLine)
}

// RemoveExcludeEntry removes the entry AddExcludeEntry wrote: the comment line and
// the line right after it, nothing a person wrote.
func RemoveExcludeEntry(path, comment, line string) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Only the entry outfit wrote: its comment line and the line right after it. A
	// "/.outfit/" the person wrote themselves, before or without outfit, stays.
	var kept []string
	removed := false
	lines := strings.Split(string(b), "\n")
	for i := 0; i < len(lines); i++ {
		if lines[i] == comment {
			removed = true
			if i+1 < len(lines) && lines[i+1] == line {
				i++
			}
			continue
		}
		kept = append(kept, lines[i])
	}
	if !removed {
		return false, nil
	}
	out := strings.Join(kept, "\n")
	return true, state.WriteFileAtomic(path, []byte(out), 0o644)
}

// Check plans without writing or locking anything: what `status` uses to say
// whether the worktree is in sync.
func Check(ctx context.Context, in Input) (*Result, error) {
	st, err := readState(in)
	if err != nil {
		return nil, err
	}
	res, _, err := in.planOnce(ctx, st)
	return res, err
}

// Merge folds the earlier passes of one converge into the final one, so that what
// the first pass found (its Lasting notes: a quarantine, a state it repaired) and did
// (the links) is still reported after a quiet last pass.
func (r *Result) Merge(earlier *Result) {
	if earlier == nil {
		return
	}
	seen := map[string]bool{}
	var notes []string
	for _, n := range append(append([]string(nil), earlier.Lasting...), r.Notes...) {
		if !seen[n] {
			seen[n] = true
			notes = append(notes, n)
		}
	}
	r.Notes = notes
	r.Lasting = append(append([]string(nil), earlier.Lasting...), r.Lasting...)
	r.Done = append(append([]plan.Action(nil), earlier.Done...), r.Done...)
	if r.Quarantine == "" {
		r.Quarantine = earlier.Quarantine
	}
	r.Changed = r.Changed || earlier.Changed
	r.NeedsHuman = r.NeedsHuman || earlier.NeedsHuman
}

// Pending reports whether the result still has work to do (the worktree is out of
// sync) as opposed to only informational notes.
//
// A RecordHash alone is bookkeeping, not a difference: the person edited the profile's
// file through the link (no hook fires for that), and nothing in the tree needs fixing.
func (r *Result) Pending() bool {
	if len(r.Refused) > 0 {
		return true
	}
	for _, a := range r.Actions {
		if !a.Kind.Informational() && !a.Kind.Bookkeeping() {
			return true
		}
	}
	return false
}

// Stale lists the swaps whose base file differs from when outfit swapped it.
func (r *Result) Stale() []plan.Action {
	var out []plan.Action
	for _, a := range r.Actions {
		if a.Kind == plan.Stale {
			out = append(out, a)
		}
	}
	return out
}
