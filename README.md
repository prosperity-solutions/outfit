# outfit

Your own setup of a git repository, without touching anyone else's.

```sh
curl -fsSL -H "Authorization: Bearer $(gh auth token)" https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
```

(One line, while the repository is private: it needs a GitHub login that can read it. No token once it
is public. More below.)

A **profile** changes two things, only on your machine:

- **which hook checks run** (turn the heavy one off, skip them all, add your own), and
- **a few files** (your `AGENTS.md`, your `CLAUDE.md`…) swapped in place of the committed ones.

Teammates never notice, `git status` stays truthful, and the repository itself is
never modified. outfit works with any git repository and any hook setup (husky,
lefthook, plain `.git/hooks`).

## Try it in 60 seconds

```sh
cd your-repo
outfit init                          # put outfit in front of your git hooks (they keep running)
outfit set hook.pre-commit=off       # skip the slow pre-commit hook, only for you
outfit use yolo                      # swap in a profile's files (your AGENTS.md instead of the team's)
git status                           # clean: nothing to commit, nothing for teammates to see
outfit uninstall                     # put everything back as it was
```

No repository of your own handy? `scripts/demo.sh` in a clone of this one
(`gh repo clone prosperity-solutions/outfit`) builds a throwaway repository with a husky hook and
walks through all of it in about two minutes, printing each step before it runs and checking what
it claims: init, switching a hook off, a shared profile, editing through the link, an editor's atomic
save, a new worktree, your own profile and `promote`, showing hidden profiles, the line for teammates
without outfit, and `uninstall`.

## What it is for

- **A heavy hook on a small laptop.** One pre-commit check eats 5 GB of RAM. Turn that
  hook off here, nowhere else: `outfit init`, then `outfit set hook.pre-commit=off --global`.
- **Trying your own agent instructions.** Edit your `AGENTS.md` without a diff in
  everyone's way, and when it is good, share it as a profile colleagues can opt into.
- **A permanent personal setup.** A `yolo` profile that skips most local checks. Local
  hooks are a convenience and CI is the real gate, so that is a fair choice. (Keys the
  team marks `locked`, such as a secret scan, cannot be skipped.)

## Install

The repository is private for now, so the download needs a GitHub token that can
read it. With the GitHub CLI signed in (`gh auth login`):

```sh
curl -fsSL -H "Authorization: Bearer $(gh auth token)" \
  https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
```

That puts your token on the command line, where other users of a shared machine can
see it. To keep it out, let `gh` fetch the script instead (the script then finds the
token itself):

```sh
gh api -H 'Accept: application/vnd.github.raw' repos/prosperity-solutions/outfit/contents/install.sh | sh
```

Once the repository is public, no token is needed:

```sh
curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
```

It is a single static binary for Linux (amd64, arm64) and macOS (universal), needs
git 2.36 or newer, and never uses `sudo`: it puts `outfit` in `~/.local/bin` (or next
to the one you already have; on a Mac that folder is not on your PATH yet, and the
installer prints the line to add to `~/.zshrc`). It checks a SHA-256 (which catches a
broken download) and the release signature (which catches a tampered release); the
signature check needs nothing installed first. Options, and checking a download by hand: [docs/install.md](docs/install.md).
Later, `outfit update` installs the newest release itself (after checking its
signature); see [docs/update.md](docs/update.md).
To see it work first, `scripts/demo.sh` in a clone of this repository
(`gh repo clone prosperity-solutions/outfit`) walks through it all in a throwaway repo.

## Use it

```sh
outfit init                      # put outfit in front of your git hooks (see docs/hooks.md)
outfit get biome                 # is a check on? prints on/off, exit 1 when unset
outfit set hook.pre-commit=off --global   # skip that hook on my machine, every repo
outfit set hook.pre-commit=off            # ...this clone only
outfit set hook.pre-commit=off --here     # ...this worktree only
outfit set hook.pre-commit=               # take the override away again
outfit set biome=off --global             # a free-form toggle (only hooks that ask for it)
outfit ls                        # the profiles you can switch to
outfit use yolo                  # swap in the yolo profile's files (and toggles)
outfit use base                  # back to exactly the committed files
outfit show lean                 # a profile's toggles and files, without switching
outfit new mine --from yolo      # a personal profile: local, never versioned (--here: this worktree only)
outfit promote mine              # move it into .outfit/profiles/ so git (and your team) can have it
outfit status                    # the active profile, where every value comes from, in sync or not
outfit diff AGENTS.md            # the base file differs from when it was swapped (a pull, a checkout): what changed
outfit ack AGENTS.md             # ...and that is fine: the profile's copy stays
outfit init --agents             # a Claude Code SessionStart entry that runs outfit sync
outfit init --team               # once, for the repository: a line in AGENTS.md and docs/outfit.md for teammates without outfit (commits nothing)
outfit sync                      # put the files right again (hooks run it after checkout and merge)
outfit author on                 # all profiles on disk, to edit side by side (author off hides them)
outfit uninstall                 # put the repository back as it was (--purge also deletes your own profiles)
outfit skills                    # how outfit works, written for coding agents (the binary carries it)
outfit skills install            # a pointer skill where Claude Code and other agents read skills
outfit update                    # install the latest release (--check only looks)
```

Every command takes `--json`, never asks a question, and can be run twice safely.
Errors say the exact command that fixes them.

`hook.<name>=off` (any git hook name) skips that hook for you, with no change to the
team's hooks. Other toggles, like `biome` above, only change a hook that asks for
them; your team can add this to any hook:

```sh
v=$(outfit get biome 2>/dev/null || git config -f .outfit/config --get outfit.biome)
[ "$v" = off ] && exit 0
```

It works with outfit installed and without it.

## Agent skills

Coding agents learn outfit from the binary itself: `outfit skills` lists eight short topics
and `outfit skills basics` prints the one to read first. `outfit skills install` puts a
small pointer skill in `~/.claude/skills/outfit/` and `~/.agents/skills/outfit/` (or, with
`--project`, in this worktree, kept out of commits), and `outfit skills remove` takes it
away. The pointer holds no documentation, so it never goes stale: what it points at is
the text of the outfit you have installed. Details: [docs/skills.md](docs/skills.md).

## How it decides

A value comes from the first of: your worktree, your clone, your machine, the active
profile, then the team's committed defaults in `.outfit/config`. Keys the team
`locked` always come from `.outfit/config`. Details and the file format:
[docs/config.md](docs/config.md) and [docs/hooks.md](docs/hooks.md).

## Status

Everything in the plan is in: toggles, the hook dispatcher, the release pipeline and
`outfit update`, profile file swaps (`use`, `sync`, `show`, `author`), conflict and stale-base
detection (`diff`, `ack`), `init --agents` and `init --team`, personal profiles (`new`,
`promote`), the agent skills (`skills`), and the property, fault-injection and fuzz tests.
Swaps: [docs/profiles.md](docs/profiles.md). How a release is built and checked:
[docs/release.md](docs/release.md); what protects the build: [docs/security.md](docs/security.md).
The plan is [docs/HANDOVER.md](docs/HANDOVER.md); how git actually behaves, pinned by tests, is
[docs/git-behavior.md](docs/git-behavior.md); how it is tested: [docs/testing.md](docs/testing.md).

## Develop

Every change goes through the ship workflow: see [AGENTS.md](AGENTS.md) and
[docs/ship.md](docs/ship.md).

```sh
go build ./cmd/outfit && ./outfit version
gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...
```

MIT licensed.
