package apply

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/prosperity-solutions/outfit/internal/fault"
	"github.com/prosperity-solutions/outfit/internal/profiles"
)

// Hard links. A local or worktree profile lives under the git directory, outside the worktree. A
// symlink into it makes a coding agent that follows it ask to read (or refuse to write) "outside this
// directory". So for those two tiers outfit puts a HARD LINK in the worktree instead: a second name
// for the profile's own file, which looks like an ordinary file in the folder. An in-place edit is
// the profile's edit at once; a save that writes a new file and renames it over the name (Claude Code's
// Edit tool, many editors) leaves the worktree with a copy of its own, which sync absorbs into the
// profile exactly like an editor's atomic save over a symlink. Committed profiles stay symlinks: they
// are inside the worktree already. docs/git-behavior.md (rows 56 and 57) pins what git does to one.

// osLink is os.Link; a test replaces it to play a file system that cannot hard-link.
var osLink = os.Link

// sameFile reports that a and b are the same regular file (one inode under two names).
func sameFile(a, b string) bool {
	ai, err := os.Stat(a)
	if err != nil || !ai.Mode().IsRegular() {
		return false
	}
	bi, err := os.Stat(b)
	return err == nil && bi.Mode().IsRegular() && os.SameFile(ai, bi)
}

// fileID is the identity of a file: the disk and the inode.
func fileID(fi os.FileInfo) (dev, ino uint64) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Dev), uint64(st.Ino) //nolint:unconvert // the types differ by platform
	}
	return 0, 0
}

// sameDisk reports that a path and the directory dir are on one disk, so a hard link can be made.
func sameDisk(path, dir string) bool {
	a, err := os.Stat(path)
	if err != nil {
		return false
	}
	b, err := os.Stat(dir)
	if err != nil {
		return false
	}
	ad, _ := fileID(a)
	bd, _ := fileID(b)
	return ad != 0 && ad == bd
}

// noHardMarker is a file in outfit's directory of the git dir: a hard link failed here for a reason about
// the file system (not the disk, not too many links), so outfit uses symlinks in this worktree and does not
// try again (without it, a sync would "upgrade" the symlink, fall back, and upgrade again for ever).
func (in Input) noHardMarker() string { return filepath.Join(in.W.GitDir, "outfit", "no-hard-links") }

// hardAllowed says a hard link may be made: a local or worktree profile, on a file system that has them.
func (in Input) hardAllowed() bool {
	if !in.hardTier() {
		return false
	}
	_, err := os.Lstat(in.noHardMarker())
	return err != nil
}

// hardTier says the active profile's files are hard-linked: a local or a worktree profile.
func (in Input) hardTier() bool {
	return in.Profile != nil && in.Profile.Tier != profiles.Committed
}

// ourHardLink reports that the regular file at abs is a hard link of the profile file whose absolute
// path is target (the link text of a personal profile is exactly that path).
func ourHardLink(abs, target string) bool {
	return filepath.IsAbs(target) && sameFile(abs, target)
}

// hardLinkOf finds the profile file the regular file at fileRel (below worktree) is a hard link of:
// profileRel (the swapped path it stands for) inside any personal profile. It is how a record lost to
// a kill is rebuilt.
func hardLinkOf(worktree, fileRel, profileRel string, personal []string, byBytes bool) (string, bool) {
	abs := filepath.Join(worktree, filepath.FromSlash(fileRel))
	h := ""
	if byBytes {
		h = hashFile(abs) // (git 2.36's `<path>~cruft` of a profile that was saved elsewhere since: the same text)
	}
	for _, root := range personal {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			cand := filepath.Join(root, e.Name(), filepath.FromSlash(profileRel))
			if sameFile(abs, cand) || (h != "" && hashFile(cand) == h) {
				return cand, true
			}
		}
	}
	return "", false
}

// canFallBack says a hard link could not be made for a reason about this file system (another disk, a
// file system without hard links, too many links), not about the files: a symlink does the same job.
func canFallBack(err error) bool {
	return errors.Is(err, syscall.EXDEV) || errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EMLINK) ||
		errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP)
}

// linkHard makes the hard link under outfit's temporary directory (inside the git directory, so a kill
// leaves nothing in the worktree) and renames it over abs: there is never a moment with neither a
// file nor a link, and what stood there is replaced, not written into.
func (in Input) linkHard(abs, path, variant string) error {
	if fi, err := os.Lstat(variant); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("the profile's file for %s is not a regular file", path)
	}
	tmp, err := in.tmpPath(path)
	if err != nil {
		return err
	}
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := osLink(variant, tmp); err != nil {
		return err
	}
	fault.Point("hard link made in the git dir, not yet renamed into place")
	if err := os.Rename(tmp, abs); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// tmpPath is the temporary name of a link at path, in outfit's directory of the git dir.
func (in Input) tmpPath(path string) (string, error) {
	dir := filepath.Join(in.W.GitDir, "outfit", "tmp")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	return filepath.Join(dir, tmpName(path)), nil
}

func tmpName(path string) string {
	sum := sha256.Sum256([]byte(path)) // the same name linkVia uses: one link at a time
	return hex.EncodeToString(sum[:8])
}
