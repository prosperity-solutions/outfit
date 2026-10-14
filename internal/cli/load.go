package cli

import (
	"context"
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

// load finds the repo WITHOUT git, makes the one `git config --list` call, and
// reads .outfit/config and the active profile.config from disk. This is the
// whole cost of `outfit get`: exactly one git process.
func load(ctx context.Context, e Env) (*loaded, error) {
	r, err := repo.Discover(e.Dir, e.Getenv)
	if err != nil {
		return nil, Errorf("not inside a git repository").WithFix("cd into your repository, then rerun")
	}
	if r.WorkTree == "" { // inside a hook with only GIT_DIR: hooks run at the worktree root
		r.WorkTree = e.Dir
	}
	l := &loaded{Repo: r, Git: gitx.Client{Dir: r.WorkTree}}

	res, err := l.Git.Read(ctx, "config", "--list", "--show-scope", "-z")
	if err != nil {
		return nil, Errorf("cannot read git config: %v", err).WithFix("git config --list")
	}
	l.Cfg = config.ParseGitList(res.Stdout)

	var warns []config.Warning
	team, err := config.ReadFile(filepath.Join(r.WorkTree, ".outfit", "config"), config.KindTeam)
	if err != nil {
		return nil, Errorf("cannot read .outfit/config: %v", err)
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
				Message: "the active profile does not exist on disk; treating it as base. Fix: outfit ls, then outfit use <profile>"})
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
