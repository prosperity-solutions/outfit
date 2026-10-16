# outfit

**One repo. Many outfits.**
Hot swap your agent harness and your local checks, without messing up git.

outfit swaps files in a git repository (`AGENTS.md`, `CLAUDE.md`, a skills folder, hook checks) for
your own versions. Only on your machine. `git status` stays clean and nobody else's diff changes.

Same prompt, different outfit:

```sh
$ outfit use strict                   $ outfit use yolo
$ claude -p "add a login form"        $ claude -p "add a login form"

agent reads: "Plan first. Tests       agent reads: "Be terse. Just ship."
first. Formatter. Small diffs.
Linter clean. Always."

result: plan, tests, formatter,       result: 1 file
change, tests again, linter error,    took 20s
fix, tests again, lint again,
6 files, 14 tests
took 14m 32s
```

(An illustration. An outfit is a folder: your `AGENTS.md`, `CLAUDE.md`, skills and hook switches.)

```sh
curl -fsSL https://raw.githubusercontent.com/prosperity-solutions/outfit/main/install.sh | sh
```

One line. No login, no token, no `sudo`. macOS and Linux, git 2.36+.

## How the trick works

1. **A symlink** stands where the file was, pointing into your profile.
2. **Sparse checkout** tells git to look away from that path. The committed file never changes, so
   there is no diff.
3. **Hooks** re-check after every pull, checkout and rebase, and a commit guard refuses to ever stage
   the link.

Edit through the link and you edit your profile. An editor's atomic save is caught and copied back.
Your bytes are never overwritten: if both sides changed, outfit saves both and asks. It works with any
git repository and any hook setup (husky, lefthook, plain `.git/hooks`), and your team does not need to
use it.

## Try it in 60 seconds

```sh
cd your-repo
outfit init                          # put outfit in front of your git hooks (they keep running)
outfit set hook.pre-commit=off       # skip the slow pre-commit hook, only for you
outfit ls                            # the profiles your team committed in .outfit/profiles (or: outfit new mine)
outfit use <profile>                 # swap in its files (your AGENTS.md instead of the team's)
git status                           # clean: nothing to commit, nothing for teammates to see
outfit uninstall                     # put everything back as it was
```

No repository of your own handy? `scripts/demo.sh` in a clone of this one
(`gh repo clone prosperity-solutions/outfit`) builds a throwaway repository with a husky hook and
walks through all of it in about half a minute, printing each step before it runs and checking what
it claims: init, switching a hook off, a shared profile, editing through the link, an editor's atomic
save, a new worktree, your own profile and `promote`, showing hidden profiles, the line for teammates
without outfit, and `uninstall`.

## What it is for

- **A heavy hook on a small laptop.** One pre-commit check eats 5 GB of RAM. Turn that
  hook off here, nowhere else: `outfit init`, then `outfit set hook.pre-commit=off --global`.
- **Trying your own agent instructions.** Edit your `AGENTS.md` without a diff in
  everyone's way, and when it is good, share it as a profile colleagues can opt into.
- **Your own skills directory.** A profile can replace a whole directory (`replace = .claude/skills` in
  its `profile.config`): the team's files below it are hidden for you and yours stand in, files
  the repository does not have included ([docs/profiles.md](docs/profiles.md)).
- **A permanent personal setup.** A `yolo` profile that skips most local checks. Local
  hooks are a convenience and CI is the real gate, so that is a fair choice. (Keys the
  team marks `locked`, such as a secret scan, cannot be skipped.)

## Install, in detail

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
outfit author on                 # all committed profiles on disk, to read and edit side by side (default: hidden)
outfit author on lean            # ...or just one; `author off [lean]` hides again; bare `author` says what is on disk
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

## Contributing

Issues and pull requests are welcome. Write like this page: short, concrete, a little cheeky, and a
real command instead of an adjective (the rules are in [AGENTS.md](AGENTS.md#voice)). Every change here goes through one required workflow
([docs/ship.md](docs/ship.md)) whose check, `Ship Workflow`, looks for a stamp that only that workflow
produces: a pull request from outside it stays red until a maintainer adds the `no-ship` label (the check reads
the labels when it starts, so a push after the label is what turns it green), so open an issue first
for anything big. To report a security problem privately, see
[SECURITY.md](SECURITY.md).
