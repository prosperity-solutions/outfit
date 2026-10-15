package apply

import (
	"context"
	"errors"
	"fmt"

	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// StaleBase describes one swap whose base file moved since outfit swapped it.
type StaleBase struct {
	Path     string
	Recorded string // the base blob the swap stands on
	Current  string // the base blob the index holds now
	Known    bool   // Current is one a person already acknowledged (or Recorded itself)
}

// ErrNotSwapped is returned for a path outfit has no swap recorded for.
var ErrNotSwapped = errors.New("not swapped by outfit")

// BaseOf reports the recorded and the current base blob of a swapped path.
func BaseOf(ctx context.Context, in Input, path string) (StaleBase, error) {
	st, err := readState(in)
	if err != nil {
		return StaleBase{}, err
	}
	for _, s := range st.Swaps {
		if s.Path != path {
			continue
		}
		cur, err := in.currentBlob(ctx, path)
		if err != nil {
			return StaleBase{}, err
		}
		return StaleBase{Path: path, Recorded: s.BaseBlob, Current: cur, Known: plan.Record{BaseBlob: s.BaseBlob, KnownBases: s.KnownBases}.BaseKnown(cur)}, nil
	}
	return StaleBase{}, ErrNotSwapped
}

// Ack records the base file's current blob as the one the swap stands on, so the
// "base is different" note goes away. It changes nothing else.
func Ack(ctx context.Context, in Input, path string) (changed bool, err error) {
	lock, err := AcquireLock(in.W.GitDir)
	if err != nil {
		return false, err
	}
	defer lock.Release()
	st, err := readState(in)
	if err != nil {
		return false, err
	}
	next := *st
	next.Swaps = append([]state.Swap(nil), st.Swaps...)
	for i, s := range next.Swaps {
		if s.Path != path {
			continue
		}
		cur, err := in.currentBlob(ctx, path)
		if err != nil {
			return false, err
		}
		if s.BaseBlob == cur {
			return false, nil
		}
		// The swap now stands on the current base (a new one, or one acknowledged earlier:
		// a branch switch back); the one it stood on joins the acknowledged ones.
		next.Swaps[i].BaseBlob = cur
		next.Swaps[i].KnownBases = pushKnown(s.KnownBases, s.BaseBlob, cur)
		_, err = state.Write(in.W.GitDir, &next)
		return err == nil, err
	}
	return false, ErrNotSwapped
}

// pushKnown puts blob at the front of the acknowledged bases without duplicates (and
// without the base the swap now stands on), keeping the newest eight.
func pushKnown(known []string, blob, standingOn string) []string {
	out := []string{}
	if blob != "" && blob != standingOn {
		out = append(out, blob)
	}
	for _, k := range known {
		if k != blob && k != standingOn && len(out) < 8 {
			out = append(out, k)
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

// currentBlob is the blob id the index holds for path.
func (in Input) currentBlob(ctx context.Context, path string) (string, error) {
	res, err := in.Git.Read(ctx, "--literal-pathspecs", "ls-files", "-s", "-t", "-z", "--", path)
	if err != nil {
		return "", fmt.Errorf("cannot read the index: %w", err)
	}
	e := parseLsFiles(res.Stdout)[path]
	if e.unmerged {
		return "", fmt.Errorf("%s is in the middle of a merge conflict; resolve it first: git add --sparse %s && git commit, then outfit sync", path, path)
	}
	if !e.tracked {
		return "", fmt.Errorf("%s is not tracked by git", path)
	}
	return e.blob, nil
}

// IsReplacedDir says path is a directory the active profile replaces (state.json records it).
func IsReplacedDir(in Input, path string) bool {
	st, err := readState(in)
	return err == nil && st != nil && contains(st.ReplaceDirs, path)
}

// AckDir records the team's current files below a replaced directory as the ones the replacement
// stands on, so the "the team changed files below it" note goes away. It changes nothing else.
func AckDir(ctx context.Context, in Input, dir string) (changed bool, err error) {
	lock, err := AcquireLock(in.W.GitDir)
	if err != nil {
		return false, err
	}
	defer lock.Release()
	st, err := readState(in)
	if err != nil {
		return false, err
	}
	if !contains(st.ReplaceDirs, dir) {
		return false, ErrNotSwapped
	}
	res, err := in.Git.Read(ctx, "--literal-pathspecs", "ls-files", "-s", "-t", "-z", "--", dir)
	if err != nil {
		return false, fmt.Errorf("cannot read the index: %w", err)
	}
	_, digest := dirDigest(dir, parseLsFiles(res.Stdout))
	if st.ReplaceSeen[dir] == digest {
		return false, nil
	}
	next := *st
	next.ReplaceSeen = map[string]string{}
	for k, v := range st.ReplaceSeen {
		next.ReplaceSeen[k] = v
	}
	next.ReplaceSeen[dir] = digest
	_, err = state.Write(in.W.GitDir, &next)
	return err == nil, err
}
