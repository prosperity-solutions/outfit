package hooks

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/repo"
	"github.com/prosperity-solutions/outfit/internal/state"
)

// Worktree is one checkout of the repository, known by its git directory. The
// per-worktree config lives in <GitDir>/config.worktree, which exists whether or
// not the checkout does (a worktree on an unmounted drive, or one deleted by
// hand and not yet pruned): so everything here works on those files, never by
// running git inside the checkout.
type Worktree struct {
	Path   string
	GitDir string
	Main   bool
}

// ConfigFile is the worktree's own config file.
func (w Worktree) ConfigFile() string { return filepath.Join(w.GitDir, "config.worktree") }

// Missing reports a worktree whose checkout directory is gone.
func (w Worktree) Missing() bool {
	if w.Path == "" {
		return false
	}
	_, err := os.Stat(w.Path)
	return err != nil
}

// Name is a short label for messages.
func (w Worktree) Name() string {
	if w.Main {
		return "main worktree"
	}
	return "worktree " + w.Path
}

// coreWorktree reads `core.worktree` from the repository's config file ("" when it
// has none), resolved against the common dir when relative.
func coreWorktree(common string) string {
	root, err := os.OpenRoot(common) // the config file is read from inside the common dir only
	if err != nil {
		return ""
	}
	defer func() { _ = root.Close() }()
	b, err := root.ReadFile("config")
	if err != nil {
		return ""
	}
	inCore := false
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "["):
			inCore = strings.EqualFold(strings.TrimSpace(strings.Trim(t, "[]")), "core")
		case inCore:
			k, v, ok := strings.Cut(t, "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "worktree") {
				p := strings.Trim(strings.TrimSpace(v), `"`)
				if p == "" {
					return ""
				}
				if !filepath.IsAbs(p) {
					p = filepath.Join(common, p)
				}
				return filepath.Clean(p)
			}
		}
	}
	return ""
}

// Worktrees lists the main worktree and every linked one, from the common dir.
func Worktrees(common string) []Worktree {
	mainPath := ""
	if filepath.Base(common) == ".git" {
		mainPath = filepath.Dir(common)
	} else if p := coreWorktree(common); p != "" {
		// A submodule or a --separate-git-dir repository: git records where its working
		// tree is in the repository's own config.
		mainPath = p
	}
	out := []Worktree{{Path: mainPath, GitDir: common, Main: true}}
	ents, err := os.ReadDir(filepath.Join(common, "worktrees"))
	if err != nil {
		return out
	}
	for _, e := range ents {
		gd := filepath.Join(common, "worktrees", e.Name())
		b, err := os.ReadFile(filepath.Join(gd, "gitdir"))
		if err != nil {
			continue
		}
		p := strings.TrimSpace(string(b))
		if !filepath.IsAbs(p) {
			p = filepath.Join(gd, p)
		}
		out = append(out, Worktree{Path: filepath.Dir(filepath.Clean(p)), GitDir: gd})
	}
	return out
}

// cfg runs `git config --file <file> …` (cwd-independent).
func cfg(ctx context.Context, g gitx.Client, file string, write bool, args ...string) (gitx.Result, error) {
	full := append([]string{"config", "--file", file}, args...)
	if write {
		return g.Write(ctx, full...)
	}
	return g.Read(ctx, full...)
}

// unset removes key from a config file; "not there" (git exits 5) is success, so
// a partly-failed uninstall can be rerun to completion.
func unset(ctx context.Context, g gitx.Client, file, key string) error {
	res, err := cfg(ctx, g, file, true, "--unset-all", key)
	if err != nil && res.ExitCode == 5 {
		return nil
	}
	return err
}

// hooksPathIn reads core.hooksPath from one config file.
func hooksPathIn(ctx context.Context, g gitx.Client, file string) (string, bool) {
	res, err := cfg(ctx, g, file, false, "--get", "core.hooksPath")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(res.Stdout)), true
}

// outfitSettings lists the outfit.* entries of one config file.
func outfitSettings(ctx context.Context, g gitx.Client, file, where string) []state.Setting {
	res, err := cfg(ctx, g, file, false, "--list", "-z")
	if err != nil {
		return nil
	}
	var out []state.Setting
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		k, v, _ := strings.Cut(rec, "\n")
		if strings.HasPrefix(strings.ToLower(k), "outfit.") {
			out = append(out, state.Setting{Where: where, Key: strings.ToLower(k), Value: v})
		}
	}
	return out
}

// InstallOptions are the inputs of Install.
type InstallOptions struct {
	DryRun     bool
	Binary     string // absolute path of the running outfit
	Version    string // outfit's version
	GitVersion string // number only, e.g. "2.43.0"
	Home       string // for ~ in core.hooksPath
}

// InstallResult says what init did, or with DryRun what it would do.
type InstallResult struct {
	DryRun      bool     `json:"dry_run"`
	Changed     bool     `json:"changed"`
	HooksDir    string   `json:"hooks_dir"`
	OriginalDir string   `json:"original_hooks_dir"`
	Shims       []string `json:"shims"`
	Removed     []string `json:"removed_shims"`
	// Adopted lists hooks another tool wrote into outfit's directory and init
	// moved to the team's hooks directory.
	Adopted   []string `json:"adopted_hooks"`
	Worktrees []string `json:"worktrees"`
	// MissingWorktrees are worktrees whose checkout is gone; they are configured
	// anyway, and `git worktree prune` removes them.
	MissingWorktrees []string `json:"missing_worktrees"`
	// EnabledWorktreeConfig is true when init turned extensions.worktreeConfig on.
	EnabledWorktreeConfig bool `json:"enabled_worktree_config"`
	// HooksPathChanged is true when core.hooksPath was written in some worktree.
	HooksPathChanged bool `json:"hooks_path_changed"`
}

// Install is `outfit init` for hooks: shims, extensions.worktreeConfig, and
// core.hooksPath in the per-worktree config of EVERY worktree (pre-existing
// linked worktrees would otherwise keep running without the dispatcher).
// Idempotent: a second run changes nothing. Facts uninstall needs are recorded
// BEFORE the first change, so even an init that fails halfway can be undone.
func Install(ctx context.Context, g gitx.Client, r repo.Repo, gc config.GitConfig, opt InstallOptions) (*InstallResult, error) {
	if r.WorkTree == "" {
		return nil, fmt.Errorf("this is a bare repository: outfit works on checkouts")
	}
	res, err := g.Read(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common := strings.TrimSpace(string(res.Stdout))
	ours := Dir(common)
	cg := gitx.Client{Dir: common, Env: g.Env}
	wts := Worktrees(common)

	// Refuse a foreign worktree-scope hooksPath in ANY worktree: outfit needs
	// that scope, and silently replacing it would drop that worktree's hooks.
	for _, w := range wts {
		// A value that is outfit's own (a moved repository leaves a stale one) is
		// not foreign: it is rewritten below.
		if v, ok := hooksPathIn(ctx, cg, w.ConfigFile()); ok && !ownsHooksPath(expand(v, w.Path, opt.Home), ours) {
			return nil, fmt.Errorf("core.hooksPath is already set to %q at worktree scope in the %s, which outfit needs for itself: move it to --local (git -C %s config --worktree --unset core.hooksPath; git config --local core.hooksPath %s)", v, w.Name(), shArg(w.Path), shArg(v))
		}
	}

	// Turning the extension on activates any config.worktree that already exists:
	// a dormant file would start applying (core.sparseCheckout, user.email…), and
	// uninstall could never get back to the same `git config --list`.
	if !gc.WorktreeConfig {
		for _, w := range wts {
			if keys := dormantKeys(ctx, cg, w, ours, opt.Home); len(keys) > 0 {
				return nil, fmt.Errorf("%s already holds per-worktree settings (%s) that git ignores while extensions.worktreeConfig is off; enabling it would start applying them. Review %s, move what you want into .git/config, then rerun `outfit init`", w.ConfigFile(), strings.Join(keys, ", "), w.ConfigFile())
			}
		}
	}

	origDir := OriginalDir(gc, ours, r.WorkTree, common, opt.Home)
	names := ShimNames(origDir)
	out := &InstallResult{DryRun: opt.DryRun, HooksDir: ours, OriginalDir: origDir, Shims: names,
		Removed: []string{}, Adopted: []string{}, Worktrees: []string{}, MissingWorktrees: []string{}}

	// 1. Record what uninstall needs, before changing anything.
	inst, ierr := state.ReadInstall(common)
	if ierr != nil && !errors.Is(ierr, state.ErrNone) {
		return nil, ierr
	}
	fresh := inst == nil
	if fresh {
		inst = &state.Install{WorktreeConfigWasSet: gc.WorktreeConfig}
		inst.Preexisting = append(inst.Preexisting, outfitSettings(ctx, cg, filepath.Join(common, "config"), "local")...)
		for _, w := range wts {
			inst.Preexisting = append(inst.Preexisting, outfitSettings(ctx, cg, w.ConfigFile(), filepath.Base(w.GitDir))...)
		}
	}
	inst.Binary, inst.Version = opt.Binary, opt.Version
	if fresh {
		out.Changed = true
	}
	// Bookkeeping only: a new binary path or version is not a change by itself
	// (the shims, which embed the path, are compared separately).
	if !opt.DryRun {
		if _, err := state.WriteInstall(common, inst); err != nil {
			return nil, err
		}
	}

	// 2. Hooks other tools dropped into our directory (lefthook install writes to
	// `git rev-parse --git-path hooks`, which is now ours) belong to the team's
	// directory: move them there, or they would be orphaned by uninstall.
	for _, n := range Foreign(ours) {
		out.Changed = true
		out.Adopted = append(out.Adopted, n)
		if !opt.DryRun {
			if err := adopt(filepath.Join(ours, n), filepath.Join(origDir, n)); err != nil {
				return nil, err
			}
		}
	}
	// `<hook>.old` is our own shim, renamed out of the way by lefthook: it would
	// make every later `lefthook install` fail.
	for _, n := range Leftovers(ours) {
		out.Changed = true
		out.Removed = append(out.Removed, n)
		if !opt.DryRun {
			_ = os.Remove(filepath.Join(ours, n))
		}
	}

	// 3. Shims: write what differs, remove generated files that no longer belong.
	want := map[string]string{}
	for _, n := range names {
		want[n] = Shim(n, opt.Binary)
	}
	for _, n := range sortedNames(want) {
		p := filepath.Join(ours, n)
		if old, err := os.ReadFile(p); err == nil && string(old) == want[n] {
			if fi, err := os.Stat(p); err == nil && fi.Mode()&0o111 != 0 {
				continue
			}
		}
		out.Changed = true
		if !opt.DryRun {
			if err := state.WriteFileAtomic(p, []byte(want[n]), 0o755); err != nil {
				return nil, err
			}
		}
	}
	for _, n := range Generated(ours) {
		if _, keep := want[n]; !keep {
			out.Changed = true
			out.Removed = append(out.Removed, n)
			if !opt.DryRun {
				if err := os.Remove(filepath.Join(ours, n)); err != nil {
					return nil, err
				}
			}
		}
	}

	// 4. Per-worktree config must be enabled before any config.worktree is read.
	if !gc.WorktreeConfig {
		out.EnabledWorktreeConfig = true
		out.Changed = true
		if !opt.DryRun {
			if _, err := cfg(ctx, cg, filepath.Join(common, "config"), true, "extensions.worktreeConfig", "true"); err != nil {
				return nil, err
			}
		}
	}

	// 5. core.hooksPath and state.json in every worktree.
	for _, w := range wts {
		out.Worktrees = append(out.Worktrees, w.Name())
		if w.Missing() {
			out.MissingWorktrees = append(out.MissingWorktrees, w.Path)
		}
		if v, ok := hooksPathIn(ctx, cg, w.ConfigFile()); !ok || !sameDir(v, ours) || out.EnabledWorktreeConfig {
			out.Changed = true
			out.HooksPathChanged = true
			if !opt.DryRun {
				if _, err := cfg(ctx, cg, w.ConfigFile(), true, "core.hooksPath", ours); err != nil {
					return nil, err
				}
			}
		}
		st := &state.State{GitVersion: opt.GitVersion}
		if prev, err := state.Read(w.GitDir); err == nil {
			// Keep everything sync recorded; init only refreshes the git version.
			*st = *prev
			st.GitVersion = opt.GitVersion
		}
		if opt.DryRun {
			if prev, err := state.Read(w.GitDir); err != nil || prev.GitVersion != st.GitVersion {
				out.Changed = true
			}
		} else if changed, err := state.Write(w.GitDir, st); err != nil {
			return nil, err
		} else if changed {
			out.Changed = true
		}
	}
	return out, nil
}

// adopt moves a hook (file or symlink) from outfit's directory to the team's. An
// identical file already there is simply dropped; a different one is never
// overwritten. A symlink moves as a symlink, its target made absolute first so a
// relative one keeps pointing at the same place.
func adopt(src, dst string) error {
	if dfi, err := os.Lstat(dst); err == nil {
		same := false
		if sfi, serr := os.Lstat(src); serr == nil && sfi.Mode().IsRegular() && dfi.Mode().IsRegular() {
			a, _ := os.ReadFile(src)
			b, _ := os.ReadFile(dst)
			same = string(a) == string(b)
		}
		if same {
			return os.Remove(src)
		}
		return fmt.Errorf("both %s and %s exist and differ: another tool installed a hook into outfit's directory, and the team already has one of that name; merge them by hand (keep the right one in %s), then rerun `outfit init`", src, dst, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if target, err := os.Readlink(src); err == nil {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(src), target)
		}
		if err := os.Symlink(filepath.Clean(target), dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

// ownsHooksPath reports whether a core.hooksPath value at worktree scope is
// outfit's: this repository's directory, or a stale one (a moved repository).
func ownsHooksPath(abs, ours string) bool { return sameDir(abs, ours) || IsOutfitDir(abs) }

// dormantKeys lists settings in a worktree's config.worktree other than outfit's
// own hooksPath.
func dormantKeys(ctx context.Context, g gitx.Client, w Worktree, ours, home string) []string {
	res, err := cfg(ctx, g, w.ConfigFile(), false, "--list", "-z")
	if err != nil {
		return nil
	}
	var keys []string
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		k, v, _ := strings.Cut(rec, "\n")
		if k == "" || (strings.EqualFold(k, "core.hookspath") && ownsHooksPath(expand(v, w.Path, home), ours)) {
			continue
		}
		keys = append(keys, strings.ToLower(k))
	}
	return keys
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// UninstallResult says what uninstall removed.
type UninstallResult struct {
	Changed bool     `json:"changed"`
	Removed []string `json:"removed"`
	Adopted []string `json:"adopted_hooks"`
	// Failed lists what could not be undone (exit 1).
	Failed []string `json:"failed"`
}

// Uninstall undoes Install in EVERY worktree, working on config files so a
// missing checkout cannot block it: core.hooksPath (only when it is outfit's),
// the shims, outfit.* settings that were not there before init, the state files,
// and extensions.worktreeConfig when init was the one to enable it. The hooksPath
// is unset in every worktree before any shim goes, so a dangling path can never
// silently disable the team's hooks. It does as much as it can and reports what
// it could not do.
func Uninstall(ctx context.Context, g gitx.Client, r repo.Repo, gc config.GitConfig, home string) (*UninstallResult, error) {
	res, err := g.Read(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common := strings.TrimSpace(string(res.Stdout))
	ours := Dir(common)
	cg := gitx.Client{Dir: common, Env: g.Env}
	wts := Worktrees(common)
	out := &UninstallResult{Removed: []string{}, Adopted: []string{}, Failed: []string{}}
	note := func(s string) { out.Changed = true; out.Removed = append(out.Removed, s) }
	fail := func(what string, err error) { out.Failed = append(out.Failed, what+": "+err.Error()) }

	inst, ierr := state.ReadInstall(common)
	if ierr != nil && !errors.Is(ierr, state.ErrNone) {
		return nil, ierr
	}

	// 1. hooksPath first, everywhere.
	for _, w := range wts {
		if v, ok := hooksPathIn(ctx, cg, w.ConfigFile()); ok && ownsHooksPath(expand(v, w.Path, home), ours) {
			if err := unset(ctx, cg, w.ConfigFile(), "core.hooksPath"); err != nil {
				fail("core.hooksPath in the "+w.Name(), err)
				continue
			}
			note("core.hooksPath (" + w.Name() + ")")
		}
	}

	// 2. Hooks other tools put in our directory go back to the team's directory.
	origDir := OriginalDir(gc, ours, r.WorkTree, common, home)
	for _, n := range Foreign(ours) {
		if err := adopt(filepath.Join(ours, n), filepath.Join(origDir, n)); err != nil {
			fail("hook "+n, err)
			continue
		}
		out.Adopted = append(out.Adopted, n)
		out.Changed = true
	}

	// 3. Shims and the (now empty) directories; never anything else.
	for _, n := range append(Generated(ours), Leftovers(ours)...) {
		if err := os.Remove(filepath.Join(ours, n)); err != nil {
			fail("shim "+n, err)
			continue
		}
		note("shim " + n)
	}
	if err := os.Remove(ours); err != nil && !os.IsNotExist(err) {
		fail("directory "+ours, fmt.Errorf("not empty (something else put files there): %w", err))
	}

	// 4. outfit.* settings, except what existed before init. Without an install
	// record we cannot tell which are ours: remove none.
	keep := map[state.Setting]bool{}
	if inst != nil {
		for _, s := range inst.Preexisting {
			keep[s] = true
		}
	}
	targets := []struct{ file, where string }{{filepath.Join(common, "config"), "local"}}
	for _, w := range wts {
		targets = append(targets, struct{ file, where string }{w.ConfigFile(), filepath.Base(w.GitDir)})
	}
	for _, t := range targets {
		seen := map[string]bool{}
		for _, s := range outfitSettings(ctx, cg, t.file, t.where) {
			if inst == nil || keep[s] || seen[s.Key] {
				continue
			}
			seen[s.Key] = true
			if err := unset(ctx, cg, t.file, s.Key); err != nil {
				fail(s.Key, err)
				continue
			}
			note(t.where + " " + s.Key)
		}
	}

	// 5. State files and the install record.
	for _, w := range wts {
		// The sync lock is an flock file that is never removed while outfit runs.
		_ = os.Remove(filepath.Join(filepath.Dir(state.Path(w.GitDir)), "sync.lock"))
		if err := os.Remove(state.Path(w.GitDir)); err == nil {
			note("state " + state.Path(w.GitDir))
			_ = os.Remove(filepath.Dir(state.Path(w.GitDir)))
		}
	}

	// 6. extensions.worktreeConfig, only if init enabled it and nothing per-worktree remains.
	if inst != nil && !inst.WorktreeConfigWasSet {
		empty := true
		for _, w := range wts {
			if lst, err := cfg(ctx, cg, w.ConfigFile(), false, "--list"); err == nil && len(strings.TrimSpace(string(lst.Stdout))) > 0 {
				empty = false
			}
		}
		if empty {
			if !gc.WorktreeConfig {
				// already gone (an earlier, partly failed run): nothing to do
			} else if err := unset(ctx, cg, filepath.Join(common, "config"), "extensions.worktreeConfig"); err != nil {
				fail("extensions.worktreeConfig", err)
			} else {
				note("extensions.worktreeConfig")
			}
		}
	}
	if len(out.Failed) == 0 && inst != nil {
		if err := os.Remove(state.InstallPath(common)); err == nil {
			note("install record")
		}
		_ = os.Remove(filepath.Dir(state.InstallPath(common)))
	}
	return out, nil
}
