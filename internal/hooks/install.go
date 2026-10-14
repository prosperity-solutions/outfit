package hooks

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/repo"
	"github.com/prosperity-solutions/outfit/internal/state"
)

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
	Worktrees   []string `json:"worktrees"`
	// EnabledWorktreeConfig is true when init turned extensions.worktreeConfig on.
	EnabledWorktreeConfig bool `json:"enabled_worktree_config"`
}

// worktreeList returns the checkout paths of every worktree, main first.
func worktreeList(ctx context.Context, g gitx.Client) ([]string, error) {
	res, err := g.Read(ctx, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, rec := range strings.Split(string(res.Stdout), "\x00") {
		if p, ok := strings.CutPrefix(rec, "worktree "); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// worktreeHooksPath reads core.hooksPath at --worktree scope in one worktree.
func worktreeHooksPath(ctx context.Context, wt string, env []string) (string, bool) {
	res, err := (gitx.Client{Dir: wt, Env: env}).Read(ctx, "config", "--worktree", "--get", "core.hooksPath")
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(res.Stdout)), true
}

// Install is `outfit init` for hooks: shims, extensions.worktreeConfig, and
// core.hooksPath at --worktree scope in EVERY worktree (pre-existing linked
// worktrees would otherwise keep running without the dispatcher). Idempotent:
// a second run changes nothing.
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

	for _, e := range gc.HooksPaths {
		if e.Scope == config.ScopeWorktree && !samePath(expand(e.Value, r.WorkTree, opt.Home), ours) {
			return nil, fmt.Errorf("core.hooksPath is already set to %q at worktree scope, which outfit needs for itself", e.Value)
		}
	}

	origDir, rawOrig := OriginalDir(gc, ours, r.WorkTree, common, opt.Home)
	names := ShimNames(origDir)
	out := &InstallResult{DryRun: opt.DryRun, HooksDir: ours, OriginalDir: origDir, Shims: names, Removed: []string{}}

	// Shims: write what differs, remove generated files that no longer belong.
	want := map[string]string{}
	for _, n := range names {
		want[n] = Shim(n, opt.Binary, rawOrig)
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

	// Per-worktree config must be enabled BEFORE any --worktree write.
	wasSet := gc.WorktreeConfig
	if prev, err := state.Read(r.GitDir); err == nil {
		wasSet = prev.WorktreeConfigWasSet
	}
	if !gc.WorktreeConfig {
		out.EnabledWorktreeConfig = true
		out.Changed = true
		if !opt.DryRun {
			if _, err := g.Write(ctx, "config", "extensions.worktreeConfig", "true"); err != nil {
				return nil, err
			}
		}
	}

	wts, err := worktreeList(ctx, g)
	if err != nil {
		return nil, err
	}
	out.Worktrees = wts
	for _, wt := range wts {
		if cur, ok := worktreeHooksPath(ctx, wt, g.Env); ok && samePath(cur, ours) && !out.EnabledWorktreeConfig {
			continue
		}
		out.Changed = true
		if !opt.DryRun {
			if _, err := (gitx.Client{Dir: wt, Env: g.Env}).Write(ctx, "config", "--worktree", "core.hooksPath", ours); err != nil {
				return nil, err
			}
		}
	}

	st := &state.State{GitVersion: opt.GitVersion, Binary: opt.Binary, Version: opt.Version, WorktreeConfigWasSet: wasSet}
	if prev, err := state.Read(r.GitDir); err == nil {
		st.Swaps = prev.Swaps
	}
	if opt.DryRun {
		if prev, err := state.Read(r.GitDir); err != nil || !sameState(prev, st) {
			out.Changed = true
		}
		return out, nil
	}
	if changed, err := state.Write(r.GitDir, st); err != nil {
		return nil, err
	} else if changed {
		out.Changed = true
	}
	return out, nil
}

func sameState(a, b *state.State) bool {
	return a.GitVersion == b.GitVersion && a.Binary == b.Binary && a.Version == b.Version &&
		a.WorktreeConfigWasSet == b.WorktreeConfigWasSet && len(a.Swaps) == len(b.Swaps)
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
}

// Uninstall undoes Install in EVERY worktree: core.hooksPath (only when it is
// outfit's), the shims, outfit.* config in the repo and worktree scopes, the
// state files, and extensions.worktreeConfig when init was the one to enable it.
// A dangling core.hooksPath would silently disable the team's hooks, so the
// hooksPath is unset in every worktree before any shim is removed.
func Uninstall(ctx context.Context, g gitx.Client, r repo.Repo) (*UninstallResult, error) {
	res, err := g.Read(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, err
	}
	common := strings.TrimSpace(string(res.Stdout))
	ours := Dir(common)
	out := &UninstallResult{Removed: []string{}}
	note := func(s string) { out.Changed = true; out.Removed = append(out.Removed, s) }

	wts, err := worktreeList(ctx, g)
	if err != nil {
		return nil, err
	}
	wasSet := true
	if st, err := state.Read(r.GitDir); err == nil {
		wasSet = st.WorktreeConfigWasSet
	}

	for _, wt := range wts {
		wg := gitx.Client{Dir: wt, Env: g.Env}
		if cur, ok := worktreeHooksPath(ctx, wt, g.Env); ok && samePath(cur, ours) {
			if _, err := wg.Write(ctx, "config", "--worktree", "--unset-all", "core.hooksPath"); err != nil {
				return nil, err
			}
			note("core.hooksPath (" + wt + ")")
		}
	}

	for _, n := range Generated(ours) {
		if err := os.Remove(filepath.Join(ours, n)); err != nil {
			return nil, err
		}
		note("shim " + n)
	}
	// Remove the now-empty directories, never anything else.
	_ = os.Remove(ours)
	_ = os.Remove(filepath.Dir(ours))

	// outfit.* config in the repo's own and each worktree's config.
	for _, wt := range wts {
		wg := gitx.Client{Dir: wt, Env: g.Env}
		for _, scope := range []string{"--local", "--worktree"} {
			if scope == "--worktree" {
				if cfg, err := wg.Read(ctx, "config", "--worktree", "--get-regexp", `^outfit\.`); err != nil || len(cfg.Stdout) == 0 {
					continue
				}
			}
			lst, err := wg.Read(ctx, "config", scope, "--name-only", "--list", "-z")
			if err != nil {
				continue
			}
			seen := map[string]bool{}
			for _, k := range strings.Split(string(lst.Stdout), "\x00") {
				if strings.HasPrefix(strings.ToLower(k), "outfit.") && !seen[k] {
					seen[k] = true
					if _, err := wg.Write(ctx, "config", scope, "--unset-all", k); err != nil {
						return nil, err
					}
					note(strings.ToLower(scope[2:]) + " " + k)
				}
			}
		}
	}

	// State files.
	for _, wt := range wts {
		gd := gitDirOf(wt)
		if gd == "" {
			continue
		}
		if err := os.Remove(state.Path(gd)); err == nil {
			note("state " + gd)
			_ = os.Remove(filepath.Dir(state.Path(gd)))
		}
	}

	if !wasSet {
		if clean(ctx, g, wts) {
			if _, err := g.Write(ctx, "config", "--unset-all", "extensions.worktreeConfig"); err == nil {
				note("extensions.worktreeConfig")
			}
		}
	}
	return out, nil
}

// gitDirOf finds a worktree's own git dir without git.
func gitDirOf(wt string) string {
	r, err := repo.Discover(wt, func(string) string { return "" })
	if err != nil {
		return ""
	}
	return r.GitDir
}

// clean reports whether no worktree still holds worktree-scoped config, so the
// extension can be switched off without orphaning anything.
func clean(ctx context.Context, g gitx.Client, wts []string) bool {
	for _, wt := range wts {
		res, err := (gitx.Client{Dir: wt, Env: g.Env}).Read(ctx, "config", "--worktree", "--list")
		if err == nil && len(strings.TrimSpace(string(res.Stdout))) > 0 {
			return false
		}
	}
	return true
}
