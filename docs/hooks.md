# Hooks: how outfit sits in front of your git hooks

`outfit init` installs a **dispatcher**: a tiny shell shim per hook, in
`<git-common-dir>/outfit/hooks/`. Git runs the shim, the shim runs
`outfit hook <name>`, and outfit runs **your team's own hook first**, exactly as git
would have, then its own logic. Husky, lefthook and plain `.git/hooks` all keep
working, with the same arguments, the same stdin (`pre-push` reads its refs there)
and the same exit code.

```sh
outfit init              # install (idempotent); --dry-run shows what would change
outfit init --agents     # also a Claude Code SessionStart entry that runs outfit sync
outfit init --team       # also, once for the repository, tell teammates who do not use outfit (below)
outfit uninstall         # undo it, in every worktree
```

## What `init` changes

| What | Where |
|---|---|
| a shim for **every** hook git can run in a non-bare repository (`pre-commit`, `commit-msg`, `pre-push`, `post-checkout`, …) bar five | `<git-common-dir>/outfit/hooks/` |
| `core.hooksPath` = that directory, in the **per-worktree config of every worktree** | `<git-dir>/config.worktree` |
| `extensions.worktreeConfig = true` | the repo's own config |
| what uninstall needs (was the extension already on, which `outfit.*` settings existed) | `<git-common-dir>/outfit/install.json`, written **first** |
| a small `state.json` (schema version, git version, the swapped files and what they stand on) | `<git-dir>/outfit/` of each worktree |
| with `--agents`: a SessionStart entry, and an ignore line for the personal settings file it lives in | `.claude/settings.local.json` of each worktree that has `.claude/`, and `<git-common-dir>/info/exclude` |

Nothing in the working tree changes and nothing is committed. Worktree scope beats
`--local`, so husky rewriting `core.hooksPath` on every `npm install` cannot win. The
path is absolute: a relative one would resolve per worktree, and in linked worktrees
(where `.git` is a file) no hook would run at all.

**Every** hook is shimmed, not only the ones your team has today: git only runs
hooks that exist in `core.hooksPath`, so a fixed set would silently skip a secret scan
added next week, or husky's hooks when `npm install` runs after `outfit init`. Five
hooks are shimmed only when the team has one: three that git runs on nearly every ref
update or index write (`reference-transaction`, `post-index-change`,
`fsmonitor-watchman`, where a shim would cost a process each time) and two whose mere
existence changes what git does (`push-to-checkout`, `proc-receive`).

`init` writes to each worktree's `config.worktree` file directly, so a worktree whose
checkout is gone (an unmounted drive, a directory deleted by hand) is configured too;
`git worktree prune` removes the leftovers. It refuses, naming the worktree, when
another tool already set `core.hooksPath` at worktree scope.

`uninstall` unsets `core.hooksPath` in every worktree *before* it removes the shims, so
a dangling path can never silently disable your hooks, then removes the `outfit.*`
settings made since init (those that were there before are kept), the SessionStart entry and its ignore line, the state files and
`extensions.worktreeConfig` if init was the one to enable it. `git config --list` comes
back exactly (a test compares it). Your local and worktree profiles are kept (`--purge` deletes them). If a link or base file cannot be put right it stops there (exit 1, listing it): no hooks, settings or profiles are touched, sparse checkout stays on in that worktree and the record stays, so the rerun continues (worktrees whose links did come off stay undone). Otherwise it does as much as it can and exits 1 listing anything it could not undo.

## `init --team`: telling the people who do not use outfit

Whoever introduces outfit to a repository runs `outfit init --team` once. It adds one line to the
**base** `AGENTS.md` (*"Optional: this repo supports outfit profiles; see docs/outfit.md to
install."*) and writes `docs/outfit.md` (what outfit is, the install command for while the
repository is private and for once it is public, the first commands, and the snippet a hook uses to
read a toggle with or without outfit). Nothing is committed: the command prints the `git add` and
`git commit` to run. It is idempotent (the line is added once; an existing `docs/outfit.md` is left
exactly as it is, so a team's own edits are never overwritten), never writes through a link, and when
`AGENTS.md` is swapped by your active profile it writes none of the team's files (exit 3, after the
hooks were installed as usual: `outfit use base` first, so the team's file is the one edited). A link
the team itself committed (`AGENTS.md -> CLAUDE.md`) is followed while it stays inside the worktree;
one that cannot be followed, or no `AGENTS.md` at all, gets the document and the line printed for you
to place. Every check runs before the first write and the document is written before the line that
points to it, so a failure never leaves the line without its document. `--dry-run` says what it would do. Nothing else changes for non-users
(docs/HANDOVER.md §3.0).

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

`pre-commit` refuses a commit when a
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

- `init` refuses for bare repositories, and when `config.worktree` already holds
  settings (turning the extension on would start applying them).
- Hooks another tool (husky, lefthook) adds later work immediately; a **new git hook
  name** git itself invents is shimmed the next time you run `outfit init`.
- **Tools that install hooks where git says hooks live** (`git rev-parse --git-path
  hooks`, which is outfit's directory after `init`) fail or misplace them:
  - `lefthook install` renames our shim to `<hook>.old` and writes its own; `outfit
    init` (and `outfit status`, which exits 4) notices, moves lefthook's hooks to your
    hooks directory and removes the leftover.
  - `git lfs install` refuses ("Hook already exists") because every hook has a shim.
    Run it as `git -c core.hooksPath=<your hooks dir> lfs install --local`, or
    `outfit uninstall && git lfs install && outfit init`.
  - pre-commit.com's `pre-commit install` refuses while `core.hooksPath` is set: use
    `outfit uninstall && pre-commit install && outfit init`.
- The guard covers **command-line git**. A GUI or IDE client that runs hooks itself
  (JGit, libgit2 front ends) may ignore `config.worktree` and skip the dispatcher, and
  `git commit --no-verify` skips every hook by design; the sparse-checkout config
  that hides swapped files from `git add` is the line of defence there.
- **A moved repository** keeps an absolute `core.hooksPath` pointing at the old place,
  which makes git skip every hook. `outfit status` says so (exit 4) and `outfit init`
  fixes it; `outfit sync` will repair it automatically in a later release.

## Syncing swapped files

After `post-checkout`, `post-merge` and `post-rewrite` the dispatcher runs `outfit sync`
(see [profiles.md](profiles.md)) so the swapped files follow what git just did. It runs
after the team's own hook, and its result never changes the hook's exit code: a failing
sync prints what went wrong on stderr and git carries on. On the `post-checkout` that `git worktree add` fires (all-zero old sha), in a worktree outfit has not synced yet, it first strips the `outfit.*` keys git copied from the main worktree's config and, if the main worktree has outfit's Claude Code SessionStart entry, gives the new worktree one (see [profiles.md](profiles.md)). If the team's own hook fails, the sync is skipped (git is already failing that command); `hook.post-checkout=off` skips the team's hook but the sync still runs.

## Environment variables

- `OUTFIT_HOOK_ACTIVE=<hook>@<common-dir>`: set by `outfit hook` while it runs the
  team's hook, so a hook that runs `git commit` (and so the same hook again) is not
  chained twice. It names the hook and the repository, so another repository's hooks
  are not skipped. Never set it yourself.
- `OUTFIT_ALLOW=1`: lets one commit through the pre-commit guard that refuses to
  commit a swapped file (see the guard section above).
