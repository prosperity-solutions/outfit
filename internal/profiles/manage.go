package profiles

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/plan"
	"github.com/prosperity-solutions/outfit/internal/repo"
)

// Limits on what is copied: a profile is a handful of small text files, and a
// profile someone else committed must not be able to fill the disk.
const (
	maxCopyFiles = 2000
	maxCopyFile  = 32 << 20
)

// Template is the profile.config of a profile made from nothing.
const Template = "# Toggles for this profile; the files next to this one replace the tracked files\n# at the same relative paths while the profile is active.\n[outfit]\n\t# biome = off\n"

// Resolve finds a profile by `name` or, to reach one a committed profile of the same
// name shadows, `tier:name` (committed, local or worktree).
func Resolve(l Listing, ref string) (Profile, error) {
	tier, name, qualified := strings.Cut(ref, ":")
	if !qualified {
		name, tier = ref, ""
	}
	if err := ValidName(name); err != nil {
		return Profile{}, err
	}
	switch Tier(tier) {
	case "", Committed, Local, Worktree:
	default:
		return Profile{}, fmt.Errorf("%q is not a tier: use committed, local or worktree", tier)
	}
	for _, p := range append(append([]Profile(nil), l.Profiles...), l.Shadowed...) {
		if p.Name == name && (tier == "" || string(p.Tier) == tier) {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("no profile called %q (committed, local or worktree)", ref)
}

// Exists reports whether any tier (the shadowed ones included) uses name.
func Exists(l Listing, name string) (Profile, bool) {
	for _, p := range append(append([]Profile(nil), l.Profiles...), l.Shadowed...) {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// source is one file to copy.
type source struct {
	rel  string
	exec bool
	read func() ([]byte, error)
}

// sources lists what a profile holds (profile.config included when it has one): from
// disk, or from HEAD for a committed profile that is hidden from disk.
func sources(ctx context.Context, g gitx.Client, p Profile) ([]source, error) {
	var out []source
	if p.Tier == Committed && !RealDir(p.Dir) {
		res, err := g.Read(ctx, "ls-tree", "-r", "-z", "HEAD", "--", CommittedRoot+"/"+p.Name+"/")
		if err != nil {
			return nil, err
		}
		prefix := CommittedRoot + "/" + p.Name + "/"
		for _, rec := range strings.Split(string(res.Stdout), "\x00") {
			meta, path, ok := strings.Cut(rec, "\t")
			if !ok || !strings.HasPrefix(path, prefix) {
				continue
			}
			f := strings.Fields(meta)
			if len(f) < 3 || (f[0] != "100644" && f[0] != "100755") {
				continue // links and submodules are not profile files
			}
			path, blob := path, f[2]
			out = append(out, source{rel: strings.TrimPrefix(path, prefix), exec: f[0] == "100755", read: func() ([]byte, error) {
				if sz, err := g.Read(ctx, "cat-file", "-s", blob); err == nil {
					if n, perr := strconv.Atoi(strings.TrimSpace(string(sz.Stdout))); perr == nil && n > maxCopyFile {
						return nil, fmt.Errorf("%s is larger than %d bytes", path, maxCopyFile)
					}
				}
				res, err := g.Read(ctx, "cat-file", "blob", blob)
				return res.Stdout, err
			}})
		}
	} else {
		root, err := os.OpenRoot(p.Dir)
		if err != nil {
			return nil, err
		}
		defer func() { _ = root.Close() }()
		err = filepath.WalkDir(p.Dir, func(path string, d fs.DirEntry, werr error) error {
			if werr != nil || !d.Type().IsRegular() {
				return nil // links and special files are never profile files
			}
			rel, _ := filepath.Rel(p.Dir, path)
			rel = filepath.ToSlash(rel)
			fi, ierr := d.Info()
			if ierr != nil {
				return nil
			}
			if fi.Size() > maxCopyFile {
				return fmt.Errorf("%s is larger than %d bytes", rel, maxCopyFile)
			}
			out = append(out, source{rel: rel, exec: fi.Mode()&0o111 != 0, read: func() ([]byte, error) { return readRooted(p.Dir, rel) }})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	if len(out) > maxCopyFiles {
		return nil, fmt.Errorf("profile %q has %d files; outfit copies at most %d", p.Name, len(out), maxCopyFiles)
	}
	for _, s := range out {
		if s.rel != ProfileConfig {
			if err := plan.ValidSwapPath(s.rel); err != nil {
				return nil, fmt.Errorf("profile %q: %v", p.Name, err)
			}
		}
	}
	return out, nil
}

func readRooted(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(rel)
}

// Copy is what Create and Promote report.
type Copy struct {
	Dir   string // the new profile's directory
	Files int    // files copied, profile.config included
}

// create makes parent/name with the given sources (nil: a profile of nothing), all
// inside parent: os.Root keeps every path below it, whatever a source's name says.
// The directory is made with Mkdir, so an existing one is never merged into.
func create(parent, name string, srcs []source) (Copy, error) {
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return Copy{}, err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return Copy{}, err
	}
	defer func() { _ = root.Close() }()
	if err := root.Mkdir(name, 0o750); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Copy{}, fmt.Errorf("%s already exists", filepath.Join(parent, name))
		}
		return Copy{}, err
	}
	fail := func(err error) (Copy, error) {
		_ = root.RemoveAll(name) // never leave half a profile behind
		return Copy{}, err
	}
	n := 0
	hasConfig := false
	for _, s := range srcs {
		b, err := s.read()
		if err != nil {
			return fail(fmt.Errorf("cannot read %s: %w", s.rel, err))
		}
		if err := writeIn(root, name+"/"+s.rel, b, s.exec); err != nil {
			return fail(err)
		}
		hasConfig = hasConfig || s.rel == ProfileConfig
		n++
	}
	if !hasConfig {
		if err := writeIn(root, name+"/"+ProfileConfig, []byte(Template), false); err != nil {
			return fail(err)
		}
		n++
	}
	return Copy{Dir: filepath.Join(parent, name), Files: n}, nil
}

func writeIn(root *os.Root, rel string, b []byte, exec bool) error {
	if dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel))); dir != "." {
		if err := root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	mode := os.FileMode(0o600)
	if exec {
		mode = 0o700
	}
	return root.WriteFile(rel, b, mode)
}

// Create makes a new profile in tier (Local, or Worktree) from nothing or from `from`.
// The name must be free in every tier.
func Create(ctx context.Context, g gitx.Client, r repo.Repo, l Listing, name string, tier Tier, from *Profile) (Copy, error) {
	if err := ValidName(name); err != nil {
		return Copy{}, err
	}
	if prev, ok := Exists(l, name); ok {
		return Copy{}, fmt.Errorf("a %s profile called %q already exists (names are unique across committed, local and worktree profiles)", prev.Tier, name)
	}
	if tier != Local && tier != Worktree {
		return Copy{}, fmt.Errorf("new profiles are local or worktree, not %s", tier)
	}
	var srcs []source
	if from != nil {
		var err error
		if srcs, err = sources(ctx, g, *from); err != nil {
			return Copy{}, err
		}
	}
	return create(TierDir(r, tier), name, srcs)
}

// Promote copies a local profile to .outfit/profiles/<name>/ in the worktree and then
// removes the local copy. The copy comes first: a failure leaves the local profile as
// it was.
func Promote(ctx context.Context, g gitx.Client, r repo.Repo, l Listing, name string) (Copy, error) {
	p, err := Resolve(l, "local:"+name)
	if err != nil {
		return Copy{}, err
	}
	if prev, ok := firstOther(l, p); ok {
		return Copy{}, fmt.Errorf("a %s profile called %q already exists: it wins over your local one, so there is nothing to promote under that name", prev.Tier, name)
	}
	if unsafePath(filepath.Join(r.WorkTree, ".outfit")) || (RealDir(filepath.Join(r.WorkTree, ".outfit")) && unsafePath(TierDir(r, Committed))) {
		return Copy{}, errors.New(".outfit and .outfit/profiles must be real directories, not symlinks")
	}
	srcs, err := sources(ctx, g, p)
	if err != nil {
		return Copy{}, err
	}
	if err := os.MkdirAll(filepath.Join(r.WorkTree, ".outfit"), 0o750); err != nil {
		return Copy{}, err
	}
	c, err := create(TierDir(r, Committed), name, srcs)
	if err != nil {
		return Copy{}, err
	}
	root, err := os.OpenRoot(TierDir(r, Local))
	if err != nil {
		return c, fmt.Errorf("copied to %s, but cannot open the local profiles to remove the old copy: %w", c.Dir, err)
	}
	defer func() { _ = root.Close() }()
	if err := root.RemoveAll(name); err != nil {
		return c, fmt.Errorf("copied to %s, but cannot remove the local copy %s: %w", c.Dir, p.Dir, err)
	}
	return c, nil
}

// firstOther is a profile of the same name in another tier.
func firstOther(l Listing, p Profile) (Profile, bool) {
	for _, q := range l.Profiles {
		if q.Name == p.Name && q.Tier != p.Tier {
			return q, true
		}
	}
	return Profile{}, false
}
