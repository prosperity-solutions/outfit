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
| shims for `pre-commit`, `post-checkout`, `post-merge`, `post-rewrite` and for every hook the team already has | `<git-common-dir>/outfit/hooks/` |
| `core.hooksPath` = that directory, at **worktree scope**, in **every** worktree | `config.worktree` |
| `extensions.worktreeConfig = true` | the repo's own config |
| a small `state.json` (schema version, git version, binary path) | `<git-dir>/outfit/` |

Nothing in the working tree changes and nothing is committed. Worktree scope beats
`--local`, so husky rewriting `core.hooksPath` on every `npm install` cannot win. The
path is absolute: a relative one would resolve per worktree, and in linked worktrees
(where `.git` is a file) no hook would run at all.

`uninstall` restores `git config --list` exactly (a test compares the output) and
unsets `core.hooksPath` in every worktree *before* it removes the shims, so a
dangling path can never silently disable your hooks.

## Chaining

`outfit hook <name>` finds the team's hook in the effective `core.hooksPath` that is
not outfit's (`--local`, then `--global`, then `--system`), or else in
`<git-common-dir>/hooks`; a relative path is relative to the worktree root, like git.
It passes the arguments, stdin and environment through and returns the hook's exit
code **exactly** (a signal becomes `128 + n`, a hook that cannot start `126`).
outfit's own result never changes that code.

**If outfit itself fails** (git refuses to read the config, a bug), the team's hook
still runs, with one line on stderr saying so. Failing toward *skipping* a secret
scan would be the worst outcome.

**If the outfit binary is gone**, the shim runs the team's hook directly and prints
one warning. Removing outfit must never silently turn your team's hooks off.

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
file. With nothing swapped the guard costs nothing: no extra git process.

## Cost

One git process per hook run (`git config --list`). Hooks the team does not have and
outfit does not need are not shimmed, so `reference-transaction` and friends never
pay for a process.

## Limits

- `init` refuses when `core.hooksPath` is already set at *worktree* scope by someone
  else (outfit needs that scope), and for bare repositories.
- A hook the team adds later (a new `husky` hook, say) is shimmed the next time you
  run `outfit init`; `outfit sync` will do it automatically in a later release.
