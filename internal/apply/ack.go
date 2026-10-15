package apply

import (
	"context"
	"errors"
	"fmt"

	"github.com/prosperity-solutions/outfit/internal/state"
)

// StaleBase describes one swap whose base file moved since outfit swapped it.
type StaleBase struct {
	Path     string
	Recorded string // the base blob when the link was made
	Current  string // the base blob the index holds now
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
		return StaleBase{Path: path, Recorded: s.BaseBlob, Current: cur}, nil
	}
	return StaleBase{}, ErrNotSwapped
}

// Ack records the base file's current blob as the one the swap stands on, so the
// "base changed upstream" note goes away. It changes nothing else.
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
		next.Swaps[i].BaseBlob = cur
		_, err = state.Write(in.W.GitDir, &next)
		return err == nil, err
	}
	return false, ErrNotSwapped
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
