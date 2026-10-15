// Package profiles knows where profiles live and what is in them. A profile is
// a directory: a profile.config of toggles plus files it swaps in at the same
// relative paths. There are three tiers (HANDOVER.md §3.1):
//
//	committed  <worktree>/.outfit/profiles/<name>/      versioned by git
//	local      <common-dir>/outfit/profiles/<name>/     never versioned
//	worktree   <git-dir>/outfit/worktree-profiles/<name>/  deleted with the worktree
//
// The worktree tier has its own directory name because in the MAIN worktree the
// git dir and the common dir are the same directory: a shared name would make
// the local and worktree tiers indistinguishable there (a deliberate deviation
// from the layout sketched in HANDOVER.md §3.1).
//
// Names are unique across tiers; a committed profile wins a clash.
package profiles

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/repo"
)

// Tier says who owns a profile.
type Tier string

// The three tiers, in precedence order (a committed profile shadows the rest).
const (
	Committed Tier = "committed"
	Local     Tier = "local"
	Worktree  Tier = "worktree"
)

// ProfileConfig is the toggles file inside a profile.
const ProfileConfig = "profile.config"

// CommittedRoot is where committed profiles live, relative to the worktree.
const CommittedRoot = ".outfit/profiles"

// nameRE keeps profile names safe to use as a path component.
var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ValidName reports whether name may name a profile. `base` is reserved.
func ValidName(name string) error {
	switch {
	case name == config.Base:
		return fmt.Errorf("%q is reserved: it means \"no swaps\"", name)
	case !nameRE.MatchString(name) || strings.Contains(name, ".."):
		return fmt.Errorf("invalid profile name %q: use lowercase letters, digits, dots, dashes and underscores", name)
	}
	return nil
}

// Profile is one profile in one tier.
type Profile struct {
	Name string `json:"name"`
	Tier Tier   `json:"tier"`
	// Dir is the on-disk directory (it may not exist for a committed profile
	// that is hidden from disk; see Committed's HEAD lookups).
	Dir string `json:"dir"`
}

// TierDir returns the directory that holds a tier's profiles.
func TierDir(r repo.Repo, t Tier) string {
	switch t {
	case Committed:
		return filepath.Join(r.WorkTree, filepath.FromSlash(CommittedRoot))
	case Local:
		return filepath.Join(r.CommonDir, "outfit", "profiles")
	default:
		return filepath.Join(r.GitDir, "outfit", "worktree-profiles")
	}
}

// Listing is the result of List.
type Listing struct {
	Profiles []Profile
	Warnings []config.Warning
}

// RealDir reports whether path is a real directory: not missing, not a file and
// NOT A SYMLINK. Committed paths are attacker-controlled, and a committed
// symlink must never make outfit read (or later swap) files outside the repo.
func RealDir(path string) bool {
	fi, err := os.Lstat(path)
	return err == nil && fi.IsDir()
}

// committedSafe reports whether .outfit and .outfit/profiles are real directories.
func committedSafe(r repo.Repo) bool {
	return RealDir(filepath.Join(r.WorkTree, ".outfit")) && RealDir(TierDir(r, Committed))
}

// List returns every profile across the tiers, sorted by name. Committed
// profiles are the union of what is on disk and what HEAD holds (inactive ones
// may be hidden from disk). A local or worktree profile whose name clashes with
// a committed one is dropped with a warning; the fix names a free name.
func List(ctx context.Context, g gitx.Client, r repo.Repo) Listing {
	var l Listing
	seen := map[string]Profile{}
	add := func(p Profile) {
		if prev, ok := seen[p.Name]; ok {
			l.Warnings = append(l.Warnings, config.Warning{
				Source:  string(p.Tier) + " profile " + p.Name,
				Message: fmt.Sprintf("name clashes with the %s profile %q, which wins; rename your directory %s to a free name", prev.Tier, p.Name, p.Dir),
			})
			return
		}
		seen[p.Name] = p
		l.Profiles = append(l.Profiles, p)
	}

	committed := map[string]bool{}
	if committedSafe(r) {
		var bad []string
		committed, bad = dirNames(TierDir(r, Committed))
		for _, b := range bad {
			l.Warnings = append(l.Warnings, config.Warning{Source: ".outfit/profiles/" + esc(b), Message: "not a valid profile name (lowercase letters, digits, dots, dashes, underscores): ignored"})
		}
	} else if unsafePath(filepath.Join(r.WorkTree, ".outfit")) || unsafePath(TierDir(r, Committed)) {
		l.Warnings = append(l.Warnings, config.Warning{Source: ".outfit", Message: "not read from disk: .outfit and .outfit/profiles must be real directories, not symlinks"})
	}
	for _, n := range headProfileNames(ctx, g) {
		committed[n] = true
	}
	for _, n := range sortedKeys(committed) {
		add(Profile{Name: n, Tier: Committed, Dir: filepath.Join(TierDir(r, Committed), n)})
	}
	for _, t := range []Tier{Local, Worktree} {
		names, _ := dirNames(TierDir(r, t))
		for _, n := range sortedKeys(names) {
			add(Profile{Name: n, Tier: t, Dir: filepath.Join(TierDir(r, t), n)})
		}
	}
	sort.Slice(l.Profiles, func(i, j int) bool { return l.Profiles[i].Name < l.Profiles[j].Name })
	return l
}

// Locate finds the profile `name` WITHOUT calling git (the hot path): committed
// first, then local, then worktree. ok is false when no tier has a directory.
func Locate(r repo.Repo, name string) (Profile, bool) {
	if ValidName(name) != nil {
		return Profile{}, false
	}
	for _, t := range []Tier{Committed, Local, Worktree} {
		if t == Committed && !committedSafe(r) {
			continue
		}
		d := filepath.Join(TierDir(r, t), name)
		// On a case-insensitive filesystem (APFS) "Yolo" would answer to "yolo";
		// require the exact on-disk spelling so macOS and Linux agree.
		if RealDir(d) && hasEntry(filepath.Dir(d), name) {
			return Profile{Name: name, Tier: t, Dir: d}, true
		}
	}
	return Profile{}, false
}

// LocateHead finds a COMMITTED profile that exists at HEAD but is not on disk (an
// inactive profile is hidden; so is the active one in the moment between
// `git config outfit.profile <name>` and the sync that brings it back). It returns
// the profile and its profile.config as committed. It costs git calls, so it is
// only for that rare mismatch, never the hot path.
func LocateHead(ctx context.Context, g gitx.Client, r repo.Repo, name string) (Profile, *config.File, bool) {
	if ValidName(name) != nil {
		return Profile{}, nil, false
	}
	res, err := g.Read(ctx, "ls-tree", "-r", "--name-only", "-z", "HEAD", "--", CommittedRoot+"/"+name+"/")
	if err != nil || len(res.Stdout) == 0 {
		return Profile{}, nil, false
	}
	p := Profile{Name: name, Tier: Committed, Dir: filepath.Join(TierDir(r, Committed), name)}
	cfg := &config.File{Source: CommittedRoot + "/" + name + "/" + ProfileConfig, Keys: map[string]string{}}
	for _, f := range strings.Split(string(res.Stdout), "\x00") {
		if f == CommittedRoot+"/"+name+"/"+ProfileConfig {
			cfg = HeadConfig(ctx, g, name)
		}
	}
	return p, cfg, true
}

// ReadConfig parses the profile's profile.config from disk. A missing file is
// an empty config, not an error.
func ReadConfig(p Profile) (*config.File, error) {
	path := filepath.Join(p.Dir, ProfileConfig)
	f, err := config.ReadFile(path, config.KindProfile)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return &config.File{Source: path, Keys: map[string]string{}}, nil
	}
	return f, nil
}

// Files lists the files a profile swaps in, relative with forward slashes,
// excluding profile.config. Reads disk only.
func Files(p Profile) []string {
	var out []string
	_ = filepath.WalkDir(p.Dir, func(path string, d fs.DirEntry, err error) error {
		// Regular files only: a symlink inside a committed profile could point
		// anywhere, and this list is what file swaps are made from.
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(p.Dir, path)
		if rel != ProfileConfig {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// HeadFiles lists a committed profile's files at HEAD (profile.config
// excluded), for profiles hidden from disk. nil when HEAD has none.
func HeadFiles(ctx context.Context, g gitx.Client, name string) []string {
	res, err := g.Read(ctx, "ls-tree", "-r", "--name-only", "-z", "HEAD", "--", CommittedRoot+"/"+name+"/")
	if err != nil {
		return nil
	}
	prefix := CommittedRoot + "/" + name + "/"
	var out []string
	for _, f := range strings.Split(string(res.Stdout), "\x00") {
		if rel, ok := strings.CutPrefix(f, prefix); ok && rel != ProfileConfig && rel != "" {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

// HeadConfig parses a committed profile's profile.config as committed at HEAD.
func HeadConfig(ctx context.Context, g gitx.Client, name string) *config.File {
	path := CommittedRoot + "/" + name + "/" + ProfileConfig
	// Check the size first: gitx buffers a command's whole output.
	if sz, err := g.Read(ctx, "cat-file", "-s", "HEAD:"+path); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(string(sz.Stdout))); perr == nil && n > config.MaxFileSize {
			return &config.File{Source: path, Keys: map[string]string{}, Warnings: []config.Warning{{Source: path, Message: "file is larger than the limit: ignored"}}}
		}
	}
	res, err := g.Read(ctx, "show", "HEAD:"+path)
	if err != nil {
		return &config.File{Source: path, Keys: map[string]string{}}
	}
	return config.ParseFile(strings.NewReader(string(res.Stdout)), path, config.KindProfile)
}

func headProfileNames(ctx context.Context, g gitx.Client) []string {
	// Default ls-tree output: "<mode> <type> <sha>\t<path>". Only TREES are
	// profiles: a committed file, or a symlink (mode 120000, type blob), is not.
	res, err := g.Read(ctx, "ls-tree", "-z", "HEAD", CommittedRoot+"/")
	if err != nil { // unborn HEAD, or no .outfit/ at all
		return nil
	}
	var out []string
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok || !strings.HasPrefix(meta, "040000 tree ") {
			continue
		}
		if n := filepath.Base(path); ValidName(n) == nil {
			out = append(out, n)
		}
	}
	return out
}

// unsafePath reports a path that exists but is not a real directory.
func unsafePath(path string) bool {
	_, err := os.Lstat(path)
	return err == nil && !RealDir(path)
}

// hasEntry reports whether dir lists an entry spelled exactly name.
func hasEntry(dir, name string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.Name() == name {
			return true
		}
	}
	return false
}

// dirNames lists the real subdirectories of dir that are valid profile names,
// and (separately) the directories whose names are not valid. DirEntry types are
// lstat-based, so a symlink is never a directory here.
func dirNames(dir string) (valid map[string]bool, invalid []string) {
	valid = map[string]bool{}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return valid, nil
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if ValidName(e.Name()) == nil {
			valid[e.Name()] = true
		} else {
			invalid = append(invalid, e.Name())
		}
	}
	return valid, invalid
}

// esc makes an untrusted name safe to print.
func esc(s string) string { return strings.Trim(strconv.Quote(s), `"`) }

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
