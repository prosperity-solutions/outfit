package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/prosperity-solutions/outfit/internal/config"
	"github.com/prosperity-solutions/outfit/internal/gitx"
	"github.com/prosperity-solutions/outfit/internal/profiles"
	"github.com/prosperity-solutions/outfit/internal/repo"
)

// loaded is the resolved configuration of the repository around Env.Dir.
type loaded struct {
	Repo repo.Repo
	Git  gitx.Client
	Cfg  config.GitConfig
	// Profile is the active profile's location (nil for base or when missing).
	Profile *profiles.Profile
	Result  config.Result
}

// errNotInRepo is load's answer outside a repository; `set --global` tolerates it.
var errNotInRepo = Errorf("not inside a git repository").WithFix("cd into your repository, then rerun")

// load finds the repo WITHOUT git, makes the one `git config --list` call, and
// reads .outfit/config and the active profile.config from disk. This is the
// whole cost of `outfit get`: exactly one git process.
func load(ctx context.Context, e Env) (*loaded, error) {
	r, err := repo.Discover(e.Dir, e.Getenv)
	if errors.Is(err, repo.ErrNotRepo) {
		return nil, errNotInRepo
	}
	if err != nil {
		return nil, Errorf("cannot locate the git directory: %v", err)
	}
	if r.WorkTree == "" {
		return nil, Errorf("this is a bare repository: outfit works on checkouts").WithFix("cd into a working tree")
	}
	l := &loaded{Repo: r, Git: gitx.Client{Dir: r.WorkTree}}
	if e.Getenv("GIT_DIR") != "" {
		// Hooks export GIT_DIR (possibly relative to a cwd we have left): run git
		// against the directory we resolved, so both agree on the repository.
		l.Git.Env = []string{"GIT_DIR=" + r.GitDir}
	}

	res, err := l.Git.Read(ctx, "config", "--list", "--show-scope", "-z")
	if err != nil {
		return nil, Errorf("cannot read git config: %v", err).WithFix("git config --list")
	}
	l.Cfg = config.ParseGitList(res.Stdout)
	if !l.Cfg.SawLocal {
		// git prints no local-scope entry at all when it refuses to read the
		// repository's config (safe.directory "dubious ownership"): without this
		// check `get` would quietly return the team value and ignore overrides.
		return nil, Errorf("git cannot read this repository's own config (is it owned by another user?)").
			WithFix("git config --global --add safe.directory " + r.WorkTree)
	}

	var warns []config.Warning
	var team *config.File
	if profiles.RealDir(filepath.Join(r.WorkTree, ".outfit")) {
		if team, err = config.ReadFile(filepath.Join(r.WorkTree, ".outfit", "config"), config.KindTeam); err != nil {
			return nil, Errorf("cannot read .outfit/config: %v", err)
		}
	} else if exists(filepath.Join(r.WorkTree, ".outfit")) {
		warns = append(warns, config.Warning{Source: ".outfit", Message: "not read: .outfit must be a real directory, not a symlink"})
	}

	choice, _ := config.ChooseProfile(l.Cfg)
	var pcfg *config.File
	if choice.Name != config.Base {
		if p, ok := profiles.Locate(r, choice.Name); ok {
			l.Profile = &p
			if pcfg, err = profiles.ReadConfig(p); err != nil {
				return nil, Errorf("cannot read the profile config of %q: %v", choice.Name, err)
			}
		} else {
			warns = append(warns, config.Warning{Source: "profile " + choice.Name,
				Message: "the active profile does not exist on disk; treating it as base. Fix: outfit ls, then git config --local outfit.profile <name>"})
		}
	}

	l.Result = config.Resolve(config.Input{Git: l.Cfg, Team: team, Profile: pcfg})
	l.Result.Warnings = append(l.Result.Warnings, warns...)
	return l, nil
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
