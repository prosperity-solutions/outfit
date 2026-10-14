// Package repo finds the git directories of the repository around a path
// WITHOUT executing git (HANDOVER.md §3.3). `outfit get` runs inside hooks and
// must stay fast, so it reads `.git`, `gitdir:` and `commondir` from disk.
package repo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Repo describes where a repository lives on disk.
type Repo struct {
	// WorkTree is the top of the checkout ("" when discovered via GIT_DIR alone).
	WorkTree string
	// GitDir is this worktree's own git dir (<common>/worktrees/<n> for a linked
	// worktree, the same as CommonDir otherwise).
	GitDir string
	// CommonDir is the shared git dir: hooks, objects, and the local profile tier.
	CommonDir string
}

// Linked reports whether this is a linked worktree.
func (r Repo) Linked() bool { return r.GitDir != r.CommonDir }

// ErrNotRepo is returned when no repository is found.
var ErrNotRepo = errors.New("not inside a git repository")

// Discover finds the repository. env is consulted for GIT_DIR (set inside
// hooks); pass os.Getenv. Otherwise it walks up from start looking for `.git`.
func Discover(start string, getenv func(string) string) (Repo, error) {
	if gd := getenv("GIT_DIR"); gd != "" {
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(start, gd)
		}
		gd = filepath.Clean(gd)
		common, err := commonDir(gd)
		if err != nil {
			return Repo{}, err
		}
		r := Repo{GitDir: gd, CommonDir: common}
		if wt := getenv("GIT_WORK_TREE"); wt != "" {
			r.WorkTree = wt
		} else if filepath.Base(gd) == ".git" {
			r.WorkTree = filepath.Dir(gd)
		}
		return r, nil
	}

	dir, err := filepath.Abs(start)
	if err != nil {
		return Repo{}, err
	}
	for {
		dotgit := filepath.Join(dir, ".git")
		fi, err := os.Lstat(dotgit)
		switch {
		case err == nil && fi.IsDir():
			return Repo{WorkTree: dir, GitDir: dotgit, CommonDir: dotgit}, nil
		case err == nil:
			gd, err := parseGitdirFile(dotgit)
			if err != nil {
				return Repo{}, err
			}
			common, err := commonDir(gd)
			if err != nil {
				return Repo{}, err
			}
			return Repo{WorkTree: dir, GitDir: gd, CommonDir: common}, nil
		case !errors.Is(err, os.ErrNotExist):
			return Repo{}, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Repo{}, ErrNotRepo
		}
		dir = parent
	}
}

// parseGitdirFile reads a `.git` file ("gitdir: <path>"), resolving a relative
// path against the file's directory.
func parseGitdirFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return "", fmt.Errorf("%s: no `gitdir:` line", path)
	}
	gd := strings.TrimSpace(rest)
	if gd == "" {
		return "", fmt.Errorf("%s: empty `gitdir:` line", path)
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(filepath.Dir(path), gd)
	}
	return filepath.Clean(gd), nil
}

// commonDir reads `commondir` inside a git dir. A plain git dir has none and is
// its own common dir.
func commonDir(gitDir string) (string, error) {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return gitDir, nil
	}
	if err != nil {
		return "", err
	}
	c := strings.TrimSpace(string(b))
	if c == "" {
		return gitDir, nil
	}
	if !filepath.IsAbs(c) {
		c = filepath.Join(gitDir, c)
	}
	return filepath.Clean(c), nil
}
