package apply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
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
	res, acts, err = in.planOnce(ctx, st)
	if err != nil || len(res.Refused) > 0 || !actionable(acts) {
		return res, err
	}
	return in.execute(ctx, st, acts, res)
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
	res := &Result{Actions: acts, Notes: append([]string(nil), g.messages...)}
	res.Refused = plan.Refused(acts)
	res.NeedsHuman = plan.NeedsHuman(acts)
	for _, a := range acts {
		switch a.Kind {
		case plan.Skip, plan.Conflict, plan.QuarantineBoth, plan.QuarantineFile:
			line := a.Reason
			if a.Fix != "" {
				line += "\n  fix: " + a.Fix
			}
			res.Notes = append(res.Notes, line)
		}
	}
	return res, acts, nil
}

// actionable reports whether anything has to be written or run.
func actionable(acts []plan.Action) bool {
	for _, a := range acts {
		switch a.Kind {
		case plan.Skip, plan.Conflict:
		default:
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

	for _, a := range acts {
		switch a.Kind {
		case plan.Skip, plan.Conflict, plan.RecordHash:
			// Nothing to run; RecordHash only changes the state written below.
		case plan.SetExpectFiles:
			if _, err := in.Git.Write(ctx, "config", "--worktree", "sparse.expectFilesOutsideOfPatterns", "true"); err != nil {
				return res, fmt.Errorf("cannot set sparse.expectFilesOutsideOfPatterns: %w", err)
			}
			next.ExpectFilesSet = true
			res.Changed = true
		case plan.RemoveLink:
			p := in.abs(a.Path)
			if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
				if err := os.Remove(p); err != nil {
					return res, err
				}
				res.Changed = true
			}
		case plan.QuarantineFile:
			if _, err := in.quarantine(res, a.Path, true, false); err != nil {
				return res, err
			}
			// The copy is saved (now or by an earlier run): the file may go.
			if err := os.Remove(in.abs(a.Path)); err != nil {
				return res, err
			}
			res.Changed = true
		case plan.WriteSparse:
			if err := in.writeSparse(ctx, a.Text); err != nil {
				return res, err
			}
			next.SparseSet = true
			res.Changed = true
		case plan.DisableSparse:
			if err := in.disableSparse(ctx, &next); err != nil {
				return res, err
			}
			res.Changed = true
		case plan.LinkPath:
			if err := in.link(a.Path, a.Target, a.Replaces); err != nil {
				return res, err
			}
			res.Changed = true
		case plan.Absorb:
			if err := in.absorb(a.Path, a.Target); err != nil {
				return res, err
			}
			res.Changed = true
		case plan.QuarantineBoth:
			created, err := in.quarantine(res, a.Path, true, true)
			if err != nil {
				return res, err
			}
			quarantined[a.Path] = true
			if created {
				res.Changed = true
			}
		case plan.RepairSkip:
			args := append([]string{"update-index", "--skip-worktree", "--"}, a.Paths...)
			if _, err := in.Git.Write(ctx, args...); err != nil {
				return res, fmt.Errorf("cannot restore git's skip-worktree bit on %s: %w", strings.Join(a.Paths, ", "), err)
			}
			res.Changed = true
		case plan.AddExclude:
			if err := addExclude(filepath.Join(in.W.CommonDir, "info", "exclude")); err != nil {
				return res, err
			}
			res.Changed = true
		}
	}

	// The new state records what is REALLY there now: every wanted path that is a
	// link to its variant, with what it is made of. A quarantined path keeps its OLD
	// record, so the next sync still finds the mismatch instead of absorbing over it.
	want, facts, g, err := in.gather(ctx, &state.State{})
	if err != nil {
		return res, err
	}
	next.Swaps = nil
	for _, sw := range want.Swaps {
		pf := facts.Paths[sw.Path]
		switch {
		case quarantined[sw.Path]:
			for _, old := range st.Swaps {
				if old.Path == sw.Path {
					next.Swaps = append(next.Swaps, old)
				}
			}
		case pf.FS == plan.Link && pf.LinkTarget == sw.Target && pf.Tracked && pf.VariantHash != "":
			next.Swaps = append(next.Swaps, state.Swap{Path: sw.Path, Target: sw.Target, VariantHash: pf.VariantHash, BaseBlob: g.blobs[sw.Path]})
		}
	}
	sort.Slice(next.Swaps, func(i, j int) bool { return next.Swaps[i].Path < next.Swaps[j].Path })
	next.Profile = ""
	if in.Profile != nil {
		next.Profile = in.Profile.Name
	}
	next.Author = in.Author
	next.CommittedProfiles = in.CommittedProfiles
	if changed, err := state.Write(in.W.GitDir, &next); err != nil {
		return res, err
	} else if changed {
		res.Changed = true
	}
	return res, nil
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
		return fmt.Errorf("git left files in place because they have uncommitted changes, so the switch is half done: %s\n  fix: commit or stash those changes, then: outfit sync", strings.TrimSpace(string(res.Stderr)))
	}
	b, err := os.ReadFile(filepath.Join(in.W.GitDir, "info", "sparse-checkout"))
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

// link puts the symlink for path in place: parent directories first (sparse
// checkout removes a directory whose only file was hidden, verified), the link
// made under a temporary name and renamed over the target so there is never a
// moment with neither a file nor a link. An existing regular file is removed only
// when the plan proved it an untouched copy of the committed file.
func (in Input) link(path, target string, replaces bool) error {
	abs := in.abs(path)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
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
	if prev, _ := filepath.Glob(filepath.Join(root, "*-"+key)); len(prev) > 0 {
		res.Quarantine = prev[0]
		return false, nil
	}
	res.Quarantine = filepath.Join(root, in.now().UTC().Format("20060102T150405.000000Z")+"-"+key)
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
func addExclude(path string) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(old)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += excludeComment + "\n" + excludeLine + "\n"
	return state.WriteFileAtomic(path, []byte(text), 0o644)
}

// RemoveExclude takes outfit's entry back out of .git/info/exclude, only the lines
// it added.
func RemoveExclude(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var kept []string
	removed := false
	for _, l := range strings.Split(string(b), "\n") {
		if l == excludeComment || l == excludeLine {
			removed = true
			continue
		}
		kept = append(kept, l)
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

// Pending reports whether the result still has work to do (the worktree is out of
// sync) as opposed to only informational notes.
func (r *Result) Pending() bool { return len(r.Refused) > 0 || actionable(r.Actions) }
