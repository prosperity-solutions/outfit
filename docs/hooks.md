# Hooks: how outfit sits in front of your git hooks

`outfit init` installs a **dispatcher**: a tiny shell shim per hook, in
`<git-common-dir>/outfit/hooks/`. Git runs the shim, the shim runs
`outfit hook <name>`, and outfit runs **your team's own hook first**, exactly as git
would have, then its own logic. Husky, lefthook and plain `.git/hooks` all keep
working, with the same arguments, the same stdin (`pre-push` reads its refs there)
and the same exit code.

```sh
outfit init              # install (idempotent); --dry-run shows what would change
outfit uninstall         # undo it, in every worktree
```

## What `init` changes

| What | Where |
|---|---|
| a shim for **every** client-side git hook (`pre-commit`, `commit-msg`, `pre-push`, `post-checkout`, …) | `<git-common-dir>/outfit/hooks/` |
| `core.hooksPath` = that directory, in the **per-worktree config of every worktree** | `<git-dir>/config.worktree` |
| `extensions.worktreeConfig = true` | the repo's own config |
| what uninstall needs (was the extension already on, which `outfit.*` settings existed) | `<git-common-dir>/outfit/install.json`, written **first** |
| a small `state.json` (schema version, git version, later the swapped files) | `<git-dir>/outfit/` of each worktree |

Nothing in the working tree changes and nothing is committed. Worktree scope beats
`--local`, so husky rewriting `core.hooksPath` on every `npm install` cannot win. The
path is absolute: a relative one would resolve per worktree, and in linked worktrees
(where `.git` is a file) no hook would run at all.

**Every** client hook is shimmed, not only the ones your team has today: git only runs
hooks that exist in `core.hooksPath`, so a fixed set would silently skip a secret scan
added next week, or husky's hooks when `npm install` runs after `outfit init`. The three
hooks git runs on nearly every ref update or index write (`reference-transaction`,
`post-index-change`, `fsmonitor-watchman`) are shimmed only when the team has one.

`init` writes to each worktree's `config.worktree` file directly, so a worktree whose
checkout is gone (an unmounted drive, a directory deleted by hand) is configured too;
`git worktree prune` removes the leftovers. It refuses, naming the worktree, when
another tool already set `core.hooksPath` at worktree scope.

`uninstall` unsets `core.hooksPath` in every worktree *before* it removes the shims, so
a dangling path can never silently disable your hooks, then removes the `outfit.*`
settings made since init (those that were there before are kept), the state files and
`extensions.worktreeConfig` if init was the one to enable it. `git config --list` comes
back exactly (a test compares it). It does as much as it can and exits 1 listing
anything it could not undo.

## Chaining

`outfit hook <name>` finds the team's hook in the effective `core.hooksPath` that is
not outfit's (`--local`, then `--global`, then `--system`), or else in
`<git-common-dir>/hooks`; `~/` is expanded and a relative path is relative to the
worktree root, like git. It runs it **first**, with the arguments, stdin and
environment, and returns its exit code **exactly** (a signal becomes `128 + n`, a hook
that cannot start `126`). outfit's own result never changes that code.

**If outfit itself fails** (git refuses to read the config, a bug), the team's hook
still runs, with one line on stderr saying so; the directory is then read straight from
the git config files. Failing toward *skipping* a secret scan would be the worst outcome.

**If the outfit binary is gone**, the shim runs the team's hook directly, finding it at
run time with the same rule, and prints one warning. Removing outfit must never silently
turn your team's hooks off.

**A hook that chains back into outfit** (a team hook that is a symlink to a shim, a
`core.hooksPath` that points at outfit's directory by another spelling) is refused
instead of forking forever.

Tools that install hooks where git says hooks live (`lefthook install` writes to
`git rev-parse --git-path hooks`, which is outfit's directory after `init`) put their
hooks in the wrong place: run `outfit init` again and it moves them to your hooks
directory. `outfit uninstall` does the same.

## Switching a hook off

```sh
outfit set hook.pre-commit=off --global     # this machine
outfit set hook.pre-commit=off              # this clone
```

or in a profile's `profile.config`:

```ini
[outfit "hook"]
    pre-commit = off
```

The dispatcher then skips the chained team hook entirely. Anything the team marked
`locked` in `.outfit/config` (say `locked = hook.pre-push`) cannot be skipped: every
personal override and every profile is ignored for it.

`git -c outfit.hook.pre-commit=off commit` skips it once.

## The pre-commit guard

Once profiles swap files (a later release), `pre-commit` refuses a commit when a
swapped path is staged, when one is staged as a symlink (mode `120000`, never
allowed), or when the sparse-checkout config that protects those paths is missing.
Each refusal prints the exact fix. `OUTFIT_ALLOW=1` lets a deliberate commit of a
swapped path through, and so does resolving a merge conflict on it with a regular
file. The guard runs **after** your team's pre-commit hook passed, so it also sees
what a formatter hook staged (lint-staged runs `git add`).

The swapped set is what `state.json` lists **plus** what outfit's own sparse-checkout
file hides, so losing one cannot switch the guard off. It fails **closed**: if the
state file is unreadable or outfit cannot read the git config while files are
swapped, the commit is refused with a fix. With nothing swapped it costs nothing.

## Checking on it

`outfit status` says whether the dispatcher is healthy and exits 4 when it is not: a
repository that was **moved** or renamed keeps an absolute `core.hooksPath` pointing at
the old place, which makes git skip *every* hook without a word. The fix is
`outfit init`.

## Cost

One git process (`git config --list`) per hook run, plus one for the pre-commit guard
when files are swapped. A commit runs four hooks (`pre-commit`, `prepare-commit-msg`,
`commit-msg`, `post-commit`), so expect four such processes; a hook the team does not
have is still a cheap process, not a skipped one, because that is what keeps later
additions working.

## Limits

- `init` refuses for bare repositories.
- Hooks another tool (husky, lefthook) adds later work immediately; a **new git hook
  name** git itself invents is shimmed the next time you run `outfit init`.
- `outfit sync` (a later release) will repair a moved repository automatically; until
  then `outfit status` tells you and `outfit init` fixes it.
