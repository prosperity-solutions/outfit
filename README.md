# outfit

Your own setup of a git repository, without touching anyone else's.

A **profile** changes two things, only on your machine:

- **which hook checks run** (turn the heavy one off, skip them all, add your own), and
- **a few files** (your `AGENTS.md`, your `CLAUDE.md`…) swapped in place of the committed ones.

Teammates never notice, `git status` stays truthful, and the repository itself is
never modified. outfit works with any git repository and any hook setup (husky,
lefthook, plain `.git/hooks`).

## What it is for

- **A heavy hook on a small laptop.** One pre-commit check eats 5 GB of RAM. Turn that
  hook off here, nowhere else: `outfit init`, then `outfit set hook.pre-commit=off --global`.
- **Trying your own agent instructions.** Edit your `AGENTS.md` without a diff in
  everyone's way, and when it is good, share it as a profile colleagues can opt into.
- **A permanent personal setup.** A `yolo` profile that skips most local checks. Local
  hooks are a convenience and CI is the real gate, so that is a fair choice. (Keys the
  team marks `locked`, such as a secret scan, cannot be skipped.)

## Install

> outfit is being built in the open and has no release yet. This section gets its
> one-line install command with the first release.

It is a single static binary for Linux (amd64, arm64) and macOS (universal),
needs git 2.36 or newer, and updates itself with `outfit update`.

## Use it

```sh
outfit init                      # put outfit in front of your git hooks (see docs/hooks.md)
outfit get biome                 # is a check on? prints on/off, exit 1 when unset
outfit set hook.pre-commit=off --global   # skip that hook on my machine, every repo
outfit set hook.pre-commit=off            # ...this clone only
outfit set hook.pre-commit=off --here     # ...this worktree only
outfit set hook.pre-commit=               # take the override away again
outfit set biome=off --global             # a free-form toggle (only hooks that ask for it)
outfit status                    # the active profile, and where every value comes from
outfit ls                        # the profiles you can switch to
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

## How it decides

A value comes from the first of: your worktree, your clone, your machine, the active
profile, then the team's committed defaults in `.outfit/config`. Keys the team
`locked` always come from `.outfit/config`. Details and the file format:
[docs/config.md](docs/config.md) and [docs/hooks.md](docs/hooks.md).

## Status

Under construction, one pull request at a time: toggles and the hook dispatcher are
in; profile file swaps, `outfit update` and the installer are next. The plan is
[docs/HANDOVER.md](docs/HANDOVER.md); how git actually behaves, pinned by tests, is
[docs/git-behavior.md](docs/git-behavior.md).

## Develop

Every change goes through the ship workflow: see [AGENTS.md](AGENTS.md) and
[docs/ship.md](docs/ship.md).

```sh
go build ./cmd/outfit && ./outfit version
gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...
```

MIT licensed.
