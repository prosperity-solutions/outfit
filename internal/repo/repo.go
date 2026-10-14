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
	// WorkTree is the top of the checkout ("" only for a bare repository).
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
		gd = findGitDir(start, getenv("GIT_DIR"), gd)
		common, err := commonDir(gd)
		if err != nil {
			return Repo{}, err
		}
		r := Repo{GitDir: gd, CommonDir: common}
		switch wt := getenv("GIT_WORK_TREE"); {
		case wt != "":
			r.WorkTree = wt
		case filepath.Base(gd) == ".git":
			r.WorkTree = filepath.Dir(gd)
		default:
			// A linked worktree's git dir records where its checkout is.
			if b, err := os.ReadFile(filepath.Join(gd, "gitdir")); err == nil {
				if p := strings.TrimSpace(string(b)); p != "" {
					if !filepath.IsAbs(p) { // worktree.useRelativePaths
						p = filepath.Join(gd, p)
					}
					r.WorkTree = filepath.Dir(filepath.Clean(p))
				}
			}
		}
		if r.WorkTree == "" {
			// Last resort (a separate git dir with no back-pointer): the nearest
			// ancestor with a .git entry. Never the bare cwd: a hook that `cd`s into
			// a package must not read that package's .outfit/.
			r.WorkTree = enclosingWorkTree(start)
		}
		return r, nil
	}

	dir, err := filepath.Abs(start)
	if err != nil {
		return Repo{}, err
	}
	for {
		dotgit := filepath.Join(dir, ".git")
		fi, err := os.Stat(dotgit) // follows a .git symlink to a directory
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

// findGitDir resolves a GIT_DIR value. A relative one is relative to the cwd git
// had when it exported it, which a hook may since have left, so also try each
// ancestor of start.
func findGitDir(start, raw, joined string) string {
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw)
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		cand := filepath.Join(dir, raw)
		if _, err := os.Stat(filepath.Join(cand, "HEAD")); err == nil {
			return filepath.Clean(cand)
		}
		if filepath.Dir(dir) == dir {
			return filepath.Clean(joined)
		}
	}
}

// enclosingWorkTree walks up from start to the first directory holding a `.git`
// entry. When there is none (a bare repo), it returns "".
func enclosingWorkTree(start string) string {
	for dir := start; ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}
